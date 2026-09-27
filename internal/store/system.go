package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Instance is a running Rowbird process, identified on the runs it claims.
type Instance struct {
	bun.BaseModel `bun:"table:instances,alias:i"`
	ID            string    `bun:"id,pk"`
	Hostname      string    `bun:"hostname,notnull"`
	Version       string    `bun:"version,notnull"`
	KeyID         string    `bun:"key_id,notnull"`
	StartedAt     time.Time `bun:"started_at,notnull"`
	HeartbeatAt   time.Time `bun:"heartbeat_at,notnull"`
}

// Ref points at a tenant-owned row: callers bind WorkspaceID to the context (WithWorkspace) and
// continue through the scoped repositories.
type Ref struct {
	WorkspaceID uuid.UUID `bun:"workspace_id,type:uuid"`
	ID          uuid.UUID `bun:"id,type:uuid"`
}

// Context binds the reference's workspace to ctx.
func (r Ref) Context(ctx context.Context) context.Context { return WithWorkspace(ctx, r.WorkspaceID) }

// SystemRepo is used by the scheduler and the workers, which serve every workspace. It is the one
// place where queries cross workspaces, and it only returns references: reading or changing the
// rows themselves goes through the scoped repositories with the reference's workspace (ADR-0007).
type SystemRepo struct{ s *Store }

// System returns the system repository.
func (s *Store) System() *SystemRepo { return &SystemRepo{s: s} }

// LockDueReports returns enabled reports whose next run is due, oldest first. Inside a transaction
// on Postgres the rows stay locked until it ends and other instances skip them (SKIP LOCKED); on
// SQLite the transaction already holds the database write lock.
func (r *SystemRepo) LockDueReports(ctx context.Context, at time.Time, limit int) ([]Ref, error) {
	var out []Ref
	q := r.s.conn(ctx).NewSelect().Model((*Report)(nil)).Column("rp.workspace_id", "rp.id").
		Where("rp.enabled = ?", true).Where("rp.next_run_at IS NOT NULL").Where("rp.next_run_at <= ?", at.UTC()).
		OrderExpr("rp.next_run_at, rp.id").Limit(limit)
	if r.s.dialect == Postgres {
		q = q.For("UPDATE SKIP LOCKED")
	}
	err := q.Scan(ctx, &out)
	return out, mapError(err)
}

// PendingRun is a queued run as seen by the claimer.
type PendingRun struct {
	Ref
	ReportID uuid.UUID `bun:"report_id,type:uuid"`
	Trigger  string    `bun:"trigger"`
}

// LockPendingRuns returns runs ready to be claimed, oldest first, locked like LockDueReports.
func (r *SystemRepo) LockPendingRuns(ctx context.Context, at time.Time, limit int) ([]PendingRun, error) {
	var out []PendingRun
	q := r.s.conn(ctx).NewSelect().Model((*Run)(nil)).Column("ru.workspace_id", "ru.id", "ru.report_id", "ru.trigger").
		Where("ru.status = ?", RunPending).Where("ru.available_at <= ?", at.UTC()).
		OrderExpr("ru.available_at, ru.id").Limit(limit)
	if r.s.dialect == Postgres {
		q = q.For("UPDATE SKIP LOCKED")
	}
	err := q.Scan(ctx, &out)
	return out, mapError(err)
}

// LockReport locks one report row until the transaction ends (Postgres), so that two instances
// deciding about overlapping runs of the same report take turns.
func (r *SystemRepo) LockReport(ctx context.Context, id uuid.UUID) error {
	if r.s.dialect != Postgres {
		return nil
	}
	var got uuid.UUID
	err := r.s.conn(ctx).NewSelect().Model((*Report)(nil)).Column("rp.id").Where("rp.id = ?", id).For("UPDATE").Scan(ctx, &got)
	return mapError(err)
}

// HasRunningRun reports whether a report has a run in progress.
func (r *SystemRepo) HasRunningRun(ctx context.Context, reportID uuid.UUID) (bool, error) {
	ok, err := r.s.conn(ctx).NewSelect().Model((*Run)(nil)).Where("ru.report_id = ?", reportID).
		Where("ru.status = ?", RunRunning).Exists(ctx)
	return ok, mapError(err)
}

// Claim marks a pending run as running on an instance. It reports false when another worker got
// there first.
func (r *SystemRepo) Claim(ctx context.Context, id uuid.UUID, instanceID string, at time.Time) (bool, error) {
	res, err := r.s.conn(ctx).NewUpdate().Model((*Run)(nil)).
		Set("status = ?", RunRunning).Set("instance_id = ?", instanceID).Set("started_at = ?", at.UTC()).
		Set("updated_at = ?", now()).Set("version = ru.version + 1").
		Where("ru.id = ?", id).Where("ru.status = ?", RunPending).Exec(ctx)
	if err != nil {
		return false, mapError(err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// OrphanedRuns returns running runs whose instance stopped sending heartbeats before staleBefore
// (or no longer exists), and, when exceptInstance is set, running runs of any other instance.
func (r *SystemRepo) OrphanedRuns(ctx context.Context, staleBefore time.Time, exceptInstance string) ([]Ref, error) {
	var out []Ref
	alive := r.s.conn(ctx).NewSelect().Model((*Instance)(nil)).Column("i.id").Where("i.heartbeat_at >= ?", staleBefore.UTC())
	q := r.s.conn(ctx).NewSelect().Model((*Run)(nil)).Column("ru.workspace_id", "ru.id").Where("ru.status = ?", RunRunning)
	if exceptInstance != "" {
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("ru.instance_id IS NULL").WhereOr("ru.instance_id <> ?", exceptInstance)
		})
	} else {
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("ru.instance_id IS NULL").WhereOr("ru.instance_id NOT IN (?)", alive)
		})
	}
	err := q.OrderExpr("ru.id").Scan(ctx, &out)
	return out, mapError(err)
}

// CancelRequested returns the runs of an instance that a user asked to cancel.
func (r *SystemRepo) CancelRequested(ctx context.Context, instanceID string) ([]uuid.UUID, error) {
	var out []uuid.UUID
	err := r.s.conn(ctx).NewSelect().Model((*Run)(nil)).Column("ru.id").Where("ru.status = ?", RunRunning).
		Where("ru.instance_id = ?", instanceID).Where("ru.cancel_requested_at IS NOT NULL").Scan(ctx, &out)
	return out, mapError(err)
}

// Heartbeat records that an instance is alive, registering it on first call.
func (r *SystemRepo) Heartbeat(ctx context.Context, in *Instance, at time.Time) error {
	in.HeartbeatAt = at.UTC().Truncate(time.Microsecond)
	if in.StartedAt.IsZero() {
		in.StartedAt = in.HeartbeatAt
	}
	_, err := r.s.conn(ctx).NewInsert().Model(in).On("CONFLICT (id) DO UPDATE").Set("heartbeat_at = EXCLUDED.heartbeat_at").Set("key_id = EXCLUDED.key_id").Exec(ctx)
	return mapError(err)
}

// LiveInstances returns the instances that sent a heartbeat since the given time.
func (r *SystemRepo) LiveInstances(ctx context.Context, since time.Time) ([]Instance, error) {
	var out []Instance
	err := r.s.conn(ctx).NewSelect().Model(&out).Where("i.heartbeat_at >= ?", since.UTC()).OrderExpr("i.id").Scan(ctx)
	return out, mapError(err)
}

// RemoveInstance forgets an instance that shut down cleanly, and instances silent since before
// staleBefore.
func (r *SystemRepo) RemoveInstance(ctx context.Context, id string, staleBefore time.Time) error {
	_, err := r.s.conn(ctx).NewDelete().Model((*Instance)(nil)).
		Where("i.id = ?", id).WhereOr("i.heartbeat_at < ?", staleBefore.UTC()).Exec(ctx)
	return mapError(err)
}

// CountPendingRuns counts the runs waiting for a worker, across workspaces (for metrics).
func (r *SystemRepo) CountPendingRuns(ctx context.Context) (int, error) {
	n, err := r.s.conn(ctx).NewSelect().Model((*Run)(nil)).Where("ru.status = ?", RunPending).Count(ctx)
	return n, mapError(err)
}

// ArtifactBytes sums the size of the stored artifacts, across workspaces (for metrics).
func (r *SystemRepo) ArtifactBytes(ctx context.Context) (int64, error) {
	var total int64
	err := r.s.conn(ctx).NewSelect().Model((*Artifact)(nil)).ColumnExpr("COALESCE(SUM(ar.size_bytes), 0)").
		Where("ar.deleted_at IS NULL").Scan(ctx, &total)
	return total, mapError(err)
}
