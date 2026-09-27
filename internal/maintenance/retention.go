// Package maintenance runs the daily retention job (docs/spec/02-data-model.md, "Retention"): old
// runs with their attempts, links and artifacts, artifact files past their retention, old security
// events, resolved notifications, and ended sessions and login challenges. A lease in the database
// makes one instance run it per day.
package maintenance

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/ids"
)

// JobRetention is the lease name of the retention job.
const JobRetention = "retention"

// Fixed retention periods (docs/spec/02-data-model.md).
const (
	SecurityEventDays = 365
	NotificationDays  = 90
	SessionGraceDays  = 30
)

const (
	every     = 24 * time.Hour
	leaseTime = time.Hour
	batch     = 200
)

// Result counts what one pass removed.
type Result struct {
	Runs, ArtifactFiles, SecurityEvents, Notifications, Sessions, Challenges int
}

// Retention is the retention job.
type Retention struct {
	store   *store.Store
	storage plugin.Storage
	backend string
	logger  *slog.Logger
	now     func() time.Time
	owner   string
	tick    time.Duration
}

// Options configure the job.
type Options struct {
	// Storage holds the artifact files of Backend; artifacts of other backends are only marked.
	Storage plugin.Storage
	Backend string
	Logger  *slog.Logger
	Now     func() time.Time
	// Tick is how often the instance checks whether the job is due (default one hour).
	Tick time.Duration
}

// New builds the job.
func New(st *store.Store, opts Options) *Retention {
	r := &Retention{store: st, storage: opts.Storage, backend: opts.Backend, logger: opts.Logger, now: opts.Now, owner: ids.New().String(), tick: opts.Tick}
	if r.logger == nil {
		r.logger = slog.New(slog.DiscardHandler)
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.tick <= 0 {
		r.tick = time.Hour
	}
	return r
}

// Run checks every tick whether the job is due and runs it when this instance gets the lease.
func (r *Retention) Run(ctx context.Context) {
	for {
		if _, _, err := r.RunIfDue(ctx); err != nil && ctx.Err() == nil {
			r.logger.ErrorContext(ctx, "retention job failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.tick):
		}
	}
}

// RunIfDue runs the job when it did not run in the last day and no other instance holds it.
func (r *Retention) RunIfDue(ctx context.Context) (Result, bool, error) {
	ok, err := r.store.Maintenance().AcquireJob(ctx, JobRetention, r.owner, r.now(), every, leaseTime)
	if err != nil || !ok {
		return Result{}, false, err
	}
	res, err := r.RunOnce(ctx)
	if err != nil {
		_ = r.store.Maintenance().ReleaseJob(context.WithoutCancel(ctx), JobRetention, r.owner)
		return res, true, err
	}
	return res, true, r.store.Maintenance().FinishJob(ctx, JobRetention, r.owner, r.now())
}

// RunOnce applies the retention rules to every workspace now.
func (r *Retention) RunOnce(ctx context.Context) (Result, error) {
	var res Result
	now := r.now().UTC()
	workspaces, err := r.store.Workspaces().List(ctx)
	if err != nil {
		return res, err
	}
	for _, w := range workspaces {
		if err := r.workspace(store.WithWorkspace(ctx, w.ID), now, &res); err != nil {
			return res, err
		}
	}
	m := r.store.Maintenance()
	if res.Sessions, err = m.DeleteEndedSessions(ctx, now.AddDate(0, 0, -SessionGraceDays)); err != nil {
		return res, err
	}
	if res.Challenges, err = m.DeleteExpiredChallenges(ctx, now); err != nil {
		return res, err
	}
	n, err := m.DeleteExpiredWebAuthnChallenges(ctx, now)
	if err != nil {
		return res, err
	}
	res.Challenges += n
	if n, err = m.DeleteEndedPasswordResets(ctx, now); err != nil {
		return res, err
	}
	res.Challenges += n
	r.logger.InfoContext(ctx, "retention job finished", "runs", res.Runs, "artifact_files", res.ArtifactFiles,
		"security_events", res.SecurityEvents, "notifications", res.Notifications, "sessions", res.Sessions)
	return res, nil
}

func (r *Retention) workspace(ctx context.Context, now time.Time, res *Result) error {
	m := r.store.Maintenance()
	runDays := r.store.Settings().Int(ctx, store.SettingRetentionRunsDays, store.DefaultRetentionRunsDays)
	artifactDays := r.store.Settings().Int(ctx, store.SettingRetentionArtifactsDays, store.DefaultRetentionArtifactsDays)

	// Old runs: their files first, then the rows (attempts, artifacts, links and downloads go
	// with them).
	for {
		runs, err := m.FinishedRunsBefore(ctx, now.AddDate(0, 0, -runDays), batch)
		if err != nil || len(runs) == 0 {
			if err != nil {
				return err
			}
			break
		}
		arts, err := m.ArtifactsOfRuns(ctx, runs)
		if err != nil {
			return err
		}
		n, err := r.removeFiles(ctx, arts, now)
		res.ArtifactFiles += n
		if err != nil {
			return err
		}
		deleted, err := m.DeleteRuns(ctx, runs)
		if err != nil {
			return err
		}
		res.Runs += deleted
		if len(runs) < batch {
			break
		}
	}
	// Artifact files past their retention; the rows stay so links answer 410 Gone.
	for {
		arts, err := m.ArtifactsCreatedBefore(ctx, now.AddDate(0, 0, -artifactDays), batch)
		if err != nil || len(arts) == 0 {
			if err != nil {
				return err
			}
			break
		}
		n, err := r.removeFiles(ctx, arts, now)
		res.ArtifactFiles += n
		if err != nil {
			return err
		}
		if len(arts) < batch {
			break
		}
	}
	n, err := m.DeleteSecurityEventsBefore(ctx, now.AddDate(0, 0, -SecurityEventDays))
	if err != nil {
		return err
	}
	res.SecurityEvents += n
	if n, err = m.DeleteNotificationsResolvedBefore(ctx, now.AddDate(0, 0, -NotificationDays)); err != nil {
		return err
	}
	res.Notifications += n
	return nil
}

// removeFiles deletes the files of the artifacts and marks them deleted. A file already gone
// counts as removed; any other storage error stops the pass, leaving the rows for the next one.
func (r *Retention) removeFiles(ctx context.Context, arts []store.Artifact, now time.Time) (int, error) {
	done := make([]uuid.UUID, 0, len(arts))
	var failed error
	for _, a := range arts {
		if r.storage != nil && a.StorageBackend == r.backend {
			if err := r.storage.Delete(ctx, a.StorageKey); err != nil && !errors.Is(err, plugin.ErrObjectNotFound) {
				failed = err
				break
			}
		}
		done = append(done, a.ID)
	}
	if err := r.store.Maintenance().MarkArtifactsDeleted(ctx, done, now); err != nil {
		return 0, err
	}
	return len(done), failed
}
