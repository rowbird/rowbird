// Package scheduler turns due reports into pending runs (ADR-0009, docs/spec/03-flows.md section
// 5). Every tick it locks the enabled reports whose next_run_at has passed and, in the same
// transaction, queues a run for that slot and moves next_run_at to the first occurrence after now.
// A report never gets two runs for one slot (the runs table has a unique index on the slot), and
// a server that was down does not enqueue every missed run: at most one catch-up run, depending on
// the report's misfire policy.
package scheduler

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/metrics"
	"github.com/rowbird/rowbird/internal/schedule"
	"github.com/rowbird/rowbird/internal/store"
)

// Defaults.
const (
	DefaultTick = 5 * time.Second
	// DefaultMisfireGrace is how late a slot may be and still count as on time.
	DefaultMisfireGrace = 2 * time.Minute
	batchSize           = 50
)

// Options configure the scheduler.
type Options struct {
	Logger       *slog.Logger
	Now          func() time.Time
	Tick         time.Duration
	MisfireGrace time.Duration
	// Wake is called after a tick queued runs.
	Wake func()
	// Metrics records how late runs are queued; nil records nothing.
	Metrics *metrics.Metrics
}

// Scheduler queues scheduled runs.
type Scheduler struct {
	store   *store.Store
	logger  *slog.Logger
	now     func() time.Time
	tick    time.Duration
	grace   time.Duration
	wake    func()
	metrics *metrics.Metrics
	// lastTick is when a tick last completed without error (Unix nanoseconds), for readiness.
	lastTick atomic.Int64
}

// New builds a scheduler.
func New(st *store.Store, opts Options) *Scheduler {
	s := &Scheduler{store: st, logger: opts.Logger, now: opts.Now, tick: opts.Tick, grace: opts.MisfireGrace, wake: opts.Wake, metrics: opts.Metrics}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.tick <= 0 {
		s.tick = DefaultTick
	}
	if s.grace <= 0 {
		s.grace = DefaultMisfireGrace
	}
	if s.wake == nil {
		s.wake = func() {}
	}
	return s
}

// Run ticks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	s.logger.InfoContext(ctx, "scheduler started", "tick", s.tick.String())
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "scheduler tick failed", "error", err)
		} else if err == nil {
			s.lastTick.Store(s.now().UnixNano())
		}
		select {
		case <-ctx.Done():
			s.logger.InfoContext(ctx, "scheduler stopped")
			return
		case <-t.C:
		}
	}
}

// Healthy reports whether a tick completed recently (within three ticks), for the readiness
// probe and the outbound heartbeat.
func (s *Scheduler) Healthy() bool {
	last := s.lastTick.Load()
	return last != 0 && s.now().Sub(time.Unix(0, last)) <= 3*s.tick
}

// Tick queues the runs of every due report and returns how many it queued.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
	total := 0
	defer func() {
		if total > 0 {
			s.wake()
		}
	}()
	for {
		now := s.now().UTC().Truncate(time.Microsecond)
		var due, queued int
		err := s.store.RunInTx(ctx, func(ctx context.Context) error {
			refs, err := s.store.System().LockDueReports(ctx, now, batchSize)
			if err != nil {
				return err
			}
			due = len(refs)
			for _, ref := range refs {
				ok, err := s.fire(ref.Context(ctx), ref.ID, now)
				if err != nil {
					return err
				}
				if ok {
					queued++
				}
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += queued
		if due < batchSize {
			return total, nil
		}
	}
}

// fire handles one due report: it queues the run for its slot (unless the slot was missed and the
// policy is to skip) and advances the schedule.
func (s *Scheduler) fire(ctx context.Context, id uuid.UUID, now time.Time) (bool, error) {
	rp, err := s.store.Reports().Get(ctx, id)
	if err != nil {
		return false, err
	}
	logger := s.logger.With("report_id", rp.ID)
	sched, err := schedule.Parse(rp.Cron, rp.Timezone)
	if err != nil {
		// Validation prevents this; stop scheduling rather than failing every tick.
		logger.ErrorContext(ctx, "report has an invalid schedule; it will not run until it is fixed", "error", err)
		return false, s.store.Reports().SetNextRun(ctx, id, nil)
	}
	slot := *rp.NextRunAt
	late := now.Sub(slot) > s.grace
	queue := !late || rp.MisfirePolicy == store.MisfireRunOnce
	if late {
		logger.WarnContext(ctx, "missed a scheduled run", "scheduled_for", slot, "misfire_policy", rp.MisfirePolicy)
	}
	if queue {
		run := &store.Run{ReportID: id, Trigger: store.TriggerSchedule, Deliver: true, ScheduledFor: &slot, AvailableAt: now}
		run.CreatedAt = now
		if err := s.store.Runs().Create(ctx, run); err != nil {
			return false, err
		}
		logger.InfoContext(ctx, "scheduled run queued", "run_id", run.ID, "scheduled_for", slot)
		s.metrics.SchedulerLag(now.Sub(slot))
	}
	var next *time.Time
	if n := sched.Next(now); !n.IsZero() {
		next = &n
	}
	return queue, s.store.Reports().SetNextRun(ctx, id, next)
}
