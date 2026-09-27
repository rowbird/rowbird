// Package runner executes queued runs (docs/spec/03-flows.md, sections 5 and 6): a pool of workers
// claims pending runs from the runs table, runs the report's query read-only through the shared
// executor, spools the rows, evaluates the condition and records the outcome. It also keeps this
// instance's heartbeat and recovers runs left behind by instances that stopped.
package runner

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/connections"
	"github.com/rowbird/rowbird/internal/delivery"
	"github.com/rowbird/rowbird/internal/metrics"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/spool"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/ids"
)

// Run error codes set by the runner. Connector and condition codes are used as they are.
const (
	CodeCancelled      = "run.cancelled"
	CodeShutdown       = "run.shutdown"
	CodeInstanceLost   = "run.instance_lost"
	CodeOverlap        = "run.overlap"
	CodeConditionFalse = "run.condition_false"
	CodeInvalidParams  = "run.invalid_params"
	CodeQueryMissing   = "run.query_missing"
	CodeInternal       = "run.internal"
	CodeDeliveryFailed = "run.delivery_failed"
)

// Tuning.
const (
	claimBatch     = 50
	maxBackoff     = time.Hour
	maxMessage     = 2000
	minStaleAfter  = 30 * time.Second
	instanceExpiry = 24 * time.Hour
)

// ResultRetention is how long a run's result stays available for download. Artifacts replace
// this in roadmap phase 6.
const ResultRetention = 24 * time.Hour

var (
	errCancelRequested = errors.New("runner: cancel requested")
	errShutdown        = errors.New("runner: shutting down")
)

// Config configures the runner.
type Config struct {
	// Workers is the number of runs executed at once.
	Workers int
	// Tick paces the heartbeat, recovery and idle polling.
	Tick time.Duration
	// SpoolDir holds result spools while runs are processed.
	SpoolDir string
	// ShutdownTimeout is how long running runs may take to finish once shutdown starts.
	ShutdownTimeout time.Duration
	// InstanceID identifies this process on the runs it claims; empty generates one.
	InstanceID string
	// KeyID is the id of the master key this instance encrypts with, recorded on its heartbeat.
	KeyID    string
	Hostname string
	Version  string
	// RecoverOnStart treats every run still marked running by another instance as lost when this
	// one starts. SQLite deployments run a single instance, so there is nobody to wait for.
	RecoverOnStart bool
}

// Runner executes runs.
type Runner struct {
	cfg      Config
	store    *store.Store
	queries  *queries.Service
	conns    *connections.Service
	delivery *delivery.Engine
	notifier Notifier
	metrics  *metrics.Metrics
	logger   *slog.Logger
	now      func() time.Time
	wake     chan struct{}

	mu       sync.Mutex
	active   map[uuid.UUID]context.CancelCauseFunc
	execBase context.Context
	instance *store.Instance
	started  bool
}

// Options carry optional collaborators.
type Options struct {
	Logger *slog.Logger
	Now    func() time.Time
	// Delivery sends finished runs to their deliveries; nil sends nothing.
	Delivery *delivery.Engine
	// Notifier learns about run changes and outcomes; nil tells nobody.
	Notifier Notifier
	// Metrics counts finished runs; nil records nothing.
	Metrics *metrics.Metrics
}

// Notifier is told about runs: every status change, for the UI, and the outcome of runs that count
// toward a report's health, for notifications and alerts.
type Notifier interface {
	RunUpdated(ctx context.Context, run *store.Run)
	RunFinished(ctx context.Context, run *store.Run, rp *store.Report, paused bool)
}

// New builds a runner.
func New(cfg Config, st *store.Store, q *queries.Service, conns *connections.Service, opts Options) *Runner {
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.Tick <= 0 {
		cfg.Tick = 5 * time.Second
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}
	if cfg.InstanceID == "" {
		cfg.InstanceID = ids.New().String()
	}
	r := &Runner{
		cfg: cfg, store: st, queries: q, conns: conns, delivery: opts.Delivery, notifier: opts.Notifier, metrics: opts.Metrics, logger: opts.Logger, now: opts.Now,
		wake: make(chan struct{}, 1), active: map[uuid.UUID]context.CancelCauseFunc{}, execBase: context.Background(),
		instance: &store.Instance{ID: cfg.InstanceID, Hostname: cfg.Hostname, Version: cfg.Version, KeyID: cfg.KeyID},
	}
	if r.logger == nil {
		r.logger = slog.New(slog.DiscardHandler)
	}
	if r.now == nil {
		r.now = time.Now
	}
	return r
}

// InstanceID identifies this runner on the runs it claims.
func (r *Runner) InstanceID() string { return r.cfg.InstanceID }

func (r *Runner) clock() time.Time { return r.now().UTC().Truncate(time.Microsecond) }

// Wake tells an idle worker to look for work now.
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Cancel stops a run executing in this process. Unknown ids are ignored: the run may be on
// another instance, which sees the request on its next tick.
func (r *Runner) Cancel(id uuid.UUID) {
	r.mu.Lock()
	cancel := r.active[id]
	r.mu.Unlock()
	if cancel != nil {
		cancel(errCancelRequested)
	}
}

// Run starts the workers and the maintenance loop and blocks until ctx is cancelled. Then it
// stops claiming, gives running runs ShutdownTimeout to finish and cancels the rest, which end as
// cancelled with run.shutdown.
func (r *Runner) Run(ctx context.Context) error {
	hard, stop := context.WithCancelCause(context.WithoutCancel(ctx))
	r.mu.Lock()
	r.execBase = hard
	r.mu.Unlock()
	if err := os.MkdirAll(r.cfg.SpoolDir, 0o700); err != nil {
		stop(nil)
		return err
	}
	r.cleanSpool()
	if err := r.Maintain(ctx); err != nil {
		r.logger.ErrorContext(ctx, "runner maintenance failed", "error", err)
	}
	r.logger.InfoContext(ctx, "runner started", "instance_id", r.cfg.InstanceID, "workers", r.cfg.Workers)

	var workers sync.WaitGroup
	for range r.cfg.Workers {
		workers.Go(func() { r.worker(ctx) })
	}
	var maint sync.WaitGroup
	maint.Go(func() {
		t := time.NewTicker(r.cfg.Tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := r.Maintain(ctx); err != nil && ctx.Err() == nil {
					r.logger.ErrorContext(ctx, "runner maintenance failed", "error", err)
				}
			}
		}
	})

	<-ctx.Done()
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(r.cfg.ShutdownTimeout):
		r.logger.WarnContext(ctx, "cancelling runs still in progress at shutdown")
		stop(errShutdown)
		<-done
	}
	stop(nil)
	maint.Wait()
	bg := context.WithoutCancel(ctx)
	if err := r.store.System().RemoveInstance(bg, r.cfg.InstanceID, r.clock().Add(-instanceExpiry)); err != nil {
		r.logger.WarnContext(ctx, "could not unregister the instance", "error", err)
	}
	r.logger.InfoContext(ctx, "runner stopped")
	return nil
}

func (r *Runner) worker(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := r.ProcessNext(ctx)
		if err != nil && ctx.Err() == nil {
			r.logger.ErrorContext(ctx, "claiming a run failed", "error", err)
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
		case <-r.wake:
		case <-time.After(r.cfg.Tick):
		}
	}
}

// ProcessNext claims one run and executes it. It reports whether there was a run to process.
func (r *Runner) ProcessNext(ctx context.Context) (bool, error) {
	run, err := r.claim(ctx)
	if err != nil || run == nil {
		return false, err
	}
	r.execute(run)
	return true, nil
}

// claim takes the oldest pending run that may start, applying the overlap policy to scheduled
// runs whose report already has a run in progress.
func (r *Runner) claim(ctx context.Context) (*store.Run, error) {
	var claimed *store.Run
	sys := r.store.System()
	err := r.store.RunInTx(ctx, func(ctx context.Context) error {
		now := r.clock()
		pending, err := sys.LockPendingRuns(ctx, now, claimBatch)
		if err != nil {
			return err
		}
		for _, p := range pending {
			wctx := p.Context(ctx)
			if p.Trigger == store.TriggerSchedule {
				if err := sys.LockReport(ctx, p.ReportID); err != nil {
					return err
				}
				busy, err := sys.HasRunningRun(ctx, p.ReportID)
				if err != nil {
					return err
				}
				if busy {
					rp, err := r.store.Reports().Get(wctx, p.ReportID)
					if err != nil {
						return err
					}
					if rp.OverlapPolicy == store.OverlapSkip {
						if _, err := r.store.Runs().FinishPending(wctx, p.ID, store.RunSkipped, CodeOverlap, now); err != nil {
							return err
						}
						r.logger.InfoContext(ctx, "scheduled run skipped: the previous run is still in progress", "run_id", p.ID, "report_id", p.ReportID)
					}
					continue
				}
			}
			ok, err := sys.Claim(ctx, p.ID, r.cfg.InstanceID, now)
			if err != nil {
				return err
			}
			if ok {
				claimed, err = r.store.Runs().Get(wctx, p.ID)
				return err
			}
		}
		return nil
	})
	return claimed, err
}

// Maintain records the heartbeat, recovers runs of instances that stopped and applies cancel
// requests made on other instances.
func (r *Runner) Maintain(ctx context.Context) error {
	sys := r.store.System()
	now := r.clock()
	if err := sys.Heartbeat(ctx, r.instance, now); err != nil {
		return err
	}
	stale := now.Add(-max(3*r.cfg.Tick, minStaleAfter))
	orphans, err := sys.OrphanedRuns(ctx, stale, "")
	if err != nil {
		return err
	}
	r.mu.Lock()
	first := !r.started
	r.started = true
	r.mu.Unlock()
	if first && r.cfg.RecoverOnStart {
		others, err := sys.OrphanedRuns(ctx, now, r.cfg.InstanceID)
		if err != nil {
			return err
		}
		orphans = append(orphans, others...)
	}
	seen := map[uuid.UUID]bool{}
	for _, ref := range orphans {
		if seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		if err := r.recoverRun(ref.Context(ctx), ref.ID); err != nil {
			r.logger.ErrorContext(ctx, "recovering a lost run failed", "run_id", ref.ID, "error", err)
		}
	}
	cancels, err := sys.CancelRequested(ctx, r.cfg.InstanceID)
	if err != nil {
		return err
	}
	for _, id := range cancels {
		r.Cancel(id)
	}
	r.cleanSpool()
	return sys.RemoveInstance(ctx, "", now.Add(-instanceExpiry))
}

// recoverRun handles a run whose instance stopped: another attempt if the report allows it,
// otherwise a failure.
func (r *Runner) recoverRun(ctx context.Context, id uuid.UUID) error {
	run, err := r.store.Runs().Get(ctx, id)
	if err != nil || run.Status != store.RunRunning || run.InstanceID == nil {
		return err
	}
	rp, err := r.store.Reports().Get(ctx, run.ReportID)
	if err != nil {
		return err
	}
	owner := *run.InstanceID
	logger := r.logger.With("run_id", run.ID, "report_id", run.ReportID, "lost_instance", owner)
	now := r.clock()
	if run.Attempt <= rp.RetryMax {
		if _, err := r.store.Runs().Release(ctx, id, owner, run.Attempt+1, now, CodeInstanceLost, ""); err != nil {
			return err
		}
		logger.WarnContext(ctx, "run lost with its instance; queued again", "attempt", run.Attempt+1)
		r.Wake()
		return nil
	}
	run.Status, run.FinishedAt = store.RunFailed, &now
	run.ErrorCode, run.ErrorMessage = ptr(CodeInstanceLost), nil
	if ok, err := r.store.Runs().Finish(ctx, run, owner); err != nil || !ok {
		return err
	}
	logger.WarnContext(ctx, "run lost with its instance")
	return r.recordOutcome(ctx, run, logger)
}

// SpoolPath is where a run's result is spooled.
func SpoolPath(dir string, runID uuid.UUID) string { return spool.Path(dir, runID) }

// cleanSpool removes spools older than ResultRetention: their runs no longer offer the download,
// and a spool left by a run that never finished is not needed either.
func (r *Runner) cleanSpool() {
	entries, err := os.ReadDir(r.cfg.SpoolDir)
	if err != nil {
		return
	}
	cutoff := r.now().Add(-ResultRetention)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !strings.HasSuffix(e.Name(), ".spool") || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(r.cfg.SpoolDir, e.Name()))
	}
}

func ptr[T any](v T) *T { return &v }
