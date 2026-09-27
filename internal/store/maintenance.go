package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MaintenanceRepo backs the daily retention job (docs/spec/02-data-model.md, "Retention"). The
// deletions are scoped to the workspace in ctx; the job lease and the global tables (sessions and
// login challenges) are not tenant-owned.
type MaintenanceRepo struct{ s *Store }

// Maintenance returns the maintenance repository.
func (s *Store) Maintenance() *MaintenanceRepo { return &MaintenanceRepo{s: s} }

type maintenanceJob struct {
	bun.BaseModel `bun:"table:maintenance_jobs,alias:mj"`
	Name          string     `bun:"name,pk"`
	LastRunAt     *time.Time `bun:"last_run_at"`
	LeaseOwner    string     `bun:"lease_owner,notnull"`
	LeaseUntil    *time.Time `bun:"lease_until"`
}

// AcquireJob takes the lease of a job that last ran before now-every and is not leased by another
// owner. It reports whether owner may run the job now.
func (r *MaintenanceRepo) AcquireJob(ctx context.Context, name, owner string, now time.Time, every, lease time.Duration) (bool, error) {
	db := r.s.conn(ctx)
	now = now.UTC()
	if _, err := db.NewInsert().Model(&maintenanceJob{Name: name}).On("CONFLICT (name) DO NOTHING").Exec(ctx); err != nil {
		return false, mapError(err)
	}
	res, err := db.NewUpdate().Model((*maintenanceJob)(nil)).
		Set("lease_owner = ?", owner).Set("lease_until = ?", now.Add(lease)).
		Where("mj.name = ?", name).
		Where("mj.lease_until IS NULL OR mj.lease_until < ?", now).
		Where("mj.last_run_at IS NULL OR mj.last_run_at <= ?", now.Add(-every)).
		Exec(ctx)
	if err != nil {
		return false, mapError(err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// FinishJob records a completed run and releases the lease.
func (r *MaintenanceRepo) FinishJob(ctx context.Context, name, owner string, at time.Time) error {
	_, err := r.s.conn(ctx).NewUpdate().Model((*maintenanceJob)(nil)).
		Set("last_run_at = ?", at.UTC()).Set("lease_owner = ''").Set("lease_until = NULL").
		Where("mj.name = ?", name).Where("mj.lease_owner = ?", owner).Exec(ctx)
	return mapError(err)
}

// ReleaseJob gives the lease back without recording a run, so another attempt can start soon.
func (r *MaintenanceRepo) ReleaseJob(ctx context.Context, name, owner string) error {
	_, err := r.s.conn(ctx).NewUpdate().Model((*maintenanceJob)(nil)).
		Set("lease_owner = ''").Set("lease_until = NULL").
		Where("mj.name = ?", name).Where("mj.lease_owner = ?", owner).Exec(ctx)
	return mapError(err)
}

// JobLastRun returns when a job last completed, nil when never.
func (r *MaintenanceRepo) JobLastRun(ctx context.Context, name string) (*time.Time, error) {
	j := new(maintenanceJob)
	err := r.s.conn(ctx).NewSelect().Model(j).Where("mj.name = ?", name).Scan(ctx)
	if err := mapError(err); errors.Is(err, ErrNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return j.LastRunAt, nil
}

// ArtifactsCreatedBefore returns stored artifacts (blob not yet removed) created before t.
func (r *MaintenanceRepo) ArtifactsCreatedBefore(ctx context.Context, t time.Time, limit int) ([]Artifact, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Artifact
	err = sc.NewSelect(&out).Where("ar.deleted_at IS NULL").Where("ar.created_at < ?", t.UTC()).
		OrderExpr("ar.created_at, ar.id").Limit(limit).Scan(ctx)
	return out, mapError(err)
}

// ArtifactsOfRuns returns the stored artifacts of the given runs.
func (r *MaintenanceRepo) ArtifactsOfRuns(ctx context.Context, runIDs []uuid.UUID) ([]Artifact, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Artifact
	if len(runIDs) == 0 {
		return out, nil
	}
	err = sc.NewSelect(&out).Where("ar.deleted_at IS NULL").Where("ar.run_id IN (?)", bun.List(runIDs)).Scan(ctx)
	return out, mapError(err)
}

// MarkArtifactsDeleted records that the blobs of the given artifacts are gone. Their rows stay, so
// links to them answer 410 Gone.
func (r *MaintenanceRepo) MarkArtifactsDeleted(ctx context.Context, ids []uuid.UUID, at time.Time) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil || len(ids) == 0 {
		return err
	}
	_, err = sc.NewUpdate((*Artifact)(nil)).Set("deleted_at = ?", at.UTC()).Where("ar.id IN (?)", bun.List(ids)).Exec(ctx)
	return mapError(err)
}

// FinishedRunsBefore returns runs in a final state created before t.
func (r *MaintenanceRepo) FinishedRunsBefore(ctx context.Context, t time.Time, limit int) ([]uuid.UUID, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []uuid.UUID
	err = sc.NewSelect((*Run)(nil)).Column("ru.id").
		Where("ru.status NOT IN (?)", bun.List([]string{RunPending, RunRunning})).
		Where("ru.created_at < ?", t.UTC()).OrderExpr("ru.created_at, ru.id").Limit(limit).Scan(ctx, &out)
	return out, mapError(err)
}

// DeleteRuns removes runs with their attempts, artifacts rows, links and link downloads.
func (r *MaintenanceRepo) DeleteRuns(ctx context.Context, ids []uuid.UUID) (int, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	res, err := sc.NewDelete((*Run)(nil)).Where("ru.id IN (?)", bun.List(ids)).Exec(ctx)
	return affected(res, err)
}

// DeleteSecurityEventsBefore removes security events older than t.
func (r *MaintenanceRepo) DeleteSecurityEventsBefore(ctx context.Context, t time.Time) (int, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return 0, err
	}
	res, err := sc.NewDelete((*SecurityEvent)(nil)).Where("?TableAlias.created_at < ?", t.UTC()).Exec(ctx)
	return affected(res, err)
}

// DeleteNotificationsResolvedBefore removes notifications resolved before t. Open ones stay, read
// or not, because new occurrences are still counted on them.
func (r *MaintenanceRepo) DeleteNotificationsResolvedBefore(ctx context.Context, t time.Time) (int, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return 0, err
	}
	res, err := sc.NewDelete((*Notification)(nil)).Where("?TableAlias.resolved_at IS NOT NULL").
		Where("?TableAlias.resolved_at < ?", t.UTC()).Exec(ctx)
	return affected(res, err)
}

// DeleteEndedSessions removes sessions that expired or were revoked before t, in every workspace.
func (r *MaintenanceRepo) DeleteEndedSessions(ctx context.Context, t time.Time) (int, error) {
	res, err := r.s.conn(ctx).NewDelete().Model((*Session)(nil)).
		Where("?TableAlias.expires_at < ? OR (?TableAlias.revoked_at IS NOT NULL AND ?TableAlias.revoked_at < ?)", t.UTC(), t.UTC()).Exec(ctx)
	return affected(res, err)
}

// DeleteExpiredChallenges removes login challenges that expired before t.
func (r *MaintenanceRepo) DeleteExpiredChallenges(ctx context.Context, t time.Time) (int, error) {
	res, err := r.s.conn(ctx).NewDelete().Model((*LoginChallenge)(nil)).Where("?TableAlias.expires_at < ?", t.UTC()).Exec(ctx)
	return affected(res, err)
}

func affected(res interface{ RowsAffected() (int64, error) }, err error) (int, error) {
	if err != nil {
		return 0, mapError(err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}
