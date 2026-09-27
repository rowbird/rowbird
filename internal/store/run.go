package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Run triggers.
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
	TriggerTest     = "test"
	TriggerRetry    = "retry"
)

// Run statuses (docs/spec/02-data-model.md).
const (
	RunPending   = "pending"
	RunRunning   = "running"
	RunSuccess   = "success"
	RunPartial   = "partial"
	RunSkipped   = "skipped"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
)

// Run is one execution of a report. The runs table is also the work queue: pending runs whose
// available_at has passed are claimed by workers (ADR-0009).
type Run struct {
	bun.BaseModel `bun:"table:runs,alias:ru"`
	TenantBase
	ReportID    uuid.UUID  `bun:"report_id,notnull,type:uuid"`
	Trigger     string     `bun:"trigger,notnull"`
	TriggeredBy *uuid.UUID `bun:"triggered_by,type:uuid"`
	// Deliver is false for manual runs that only show the result.
	Deliver      bool       `bun:"deliver,notnull"`
	Status       string     `bun:"status,notnull"`
	ScheduledFor *time.Time `bun:"scheduled_for"`
	// AvailableAt is when the run may be claimed; retries push it forward.
	AvailableAt     time.Time       `bun:"available_at,notnull"`
	StartedAt       *time.Time      `bun:"started_at"`
	FinishedAt      *time.Time      `bun:"finished_at"`
	DurationMS      *int64          `bun:"duration_ms"`
	QueryVersionID  *uuid.UUID      `bun:"query_version_id,type:uuid"`
	ResolvedParams  json.RawMessage `bun:"resolved_params,type:jsonb"`
	RowCount        *int64          `bun:"row_count"`
	Truncated       bool            `bun:"truncated,notnull"`
	ConditionResult json.RawMessage `bun:"condition_result,type:jsonb"`
	ResultHash      *string         `bun:"result_hash"`
	// ResultSample holds the columns and up to the first 100 rows, as the API encodes them.
	ResultSample      json.RawMessage `bun:"result_sample,type:jsonb"`
	Attempt           int             `bun:"attempt,notnull"`
	ErrorCode         *string         `bun:"error_code"`
	ErrorMessage      *string         `bun:"error_message"`
	InstanceID        *string         `bun:"instance_id"`
	IdempotencyKey    *string         `bun:"idempotency_key"`
	CancelRequestedAt *time.Time      `bun:"cancel_requested_at"`
	// ResultExpiresAt is set while the run's result can be downloaded (its spool is kept).
	ResultExpiresAt *time.Time `bun:"result_expires_at"`
	// TestTarget says where a test run delivers: {"channel_id": ...} or {"email": ...}.
	TestTarget json.RawMessage `bun:"test_target,type:jsonb"`
}

// Active reports whether the run has not finished.
func (r *Run) Active() bool { return r.Status == RunPending || r.Status == RunRunning }

// RunFilter narrows a run listing. Zero fields do not filter.
type RunFilter struct {
	ReportID uuid.UUID
	Statuses []string
	Trigger  string
	// From and To bound created_at (From inclusive, To exclusive).
	From, To time.Time
}

// RunRepo persists runs, scoped to the workspace in ctx.
type RunRepo struct{ s *Store }

// Runs returns the run repository.
func (s *Store) Runs() *RunRepo { return &RunRepo{s: s} }

// Create queues a run.
func (r *RunRepo) Create(ctx context.Context, run *Run) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	if run.Attempt == 0 {
		run.Attempt = 1
	}
	if run.Status == "" {
		run.Status = RunPending
	}
	return sc.Insert(ctx, run)
}

// Get returns a run by id.
func (r *RunRepo) Get(ctx context.Context, id uuid.UUID) (*Run, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	run := new(Run)
	err = sc.NewSelect(run).Where("ru.id = ?", id).Scan(ctx)
	return run, mapError(err)
}

// List returns a page of runs, newest first, without their result samples.
func (r *RunRepo) List(ctx context.Context, f RunFilter, page PageRequest) (Page[Run], error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return Page[Run]{}, err
	}
	var out []Run
	q := sc.NewSelect(&out).ExcludeColumn("result_sample", "resolved_params")
	if f.ReportID != uuid.Nil {
		q = q.Where("ru.report_id = ?", f.ReportID)
	}
	if len(f.Statuses) > 0 {
		q = q.Where("ru.status IN (?)", bun.List(f.Statuses))
	}
	if f.Trigger != "" {
		q = q.Where("ru.trigger = ?", f.Trigger)
	}
	if !f.From.IsZero() {
		q = q.Where("ru.created_at >= ?", f.From.UTC())
	}
	if !f.To.IsZero() {
		q = q.Where("ru.created_at < ?", f.To.UTC())
	}
	if q, err = paginate(q, page); err != nil {
		return Page[Run]{}, err
	}
	if err := q.Scan(ctx); err != nil {
		return Page[Run]{}, mapError(err)
	}
	return finishPage(out, page, func(r Run) uuid.UUID { return r.ID }), nil
}

// Latest returns the most recent run of each report, keyed by report id.
func (r *RunRepo) Latest(ctx context.Context) (map[uuid.UUID]Run, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Run
	// Postgres has no MAX(uuid); a correlated subquery works in both dialects.
	err = sc.NewSelect(&out).ExcludeColumn("result_sample", "resolved_params", "condition_result").
		Where("ru.id = (SELECT r2.id FROM runs AS r2 WHERE r2.report_id = ru.report_id ORDER BY r2.id DESC LIMIT 1)").Scan(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	m := make(map[uuid.UUID]Run, len(out))
	for _, run := range out {
		m[run.ReportID] = run
	}
	return m, nil
}

// CountOutcomes counts, by status, the finished runs created since the given time that count toward
// a report's health: scheduled runs and manual runs that deliver.
func (r *RunRepo) CountOutcomes(ctx context.Context, since time.Time) (map[string]int, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Status string `bun:"status"`
		N      int    `bun:"n"`
	}
	err = sc.NewSelect((*Run)(nil)).ColumnExpr("ru.status AS status, COUNT(*) AS n").
		Where("ru.created_at >= ?", since.UTC()).
		Where("(ru.trigger = ? OR (ru.trigger = ? AND ru.deliver = ?))", TriggerSchedule, TriggerManual, true).
		Where("ru.status IN (?)", bun.List([]string{RunSuccess, RunPartial, RunFailed, RunSkipped})).
		GroupExpr("ru.status").Scan(ctx, &rows)
	if err != nil {
		return nil, mapError(err)
	}
	out := map[string]int{}
	for _, row := range rows {
		out[row.Status] = row.N
	}
	return out, nil
}

// CountActive returns how many runs of a report are pending or running.
func (r *RunRepo) CountActive(ctx context.Context, reportID uuid.UUID) (int, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return 0, err
	}
	n, err := sc.NewSelect((*Run)(nil)).Where("ru.report_id = ?", reportID).
		Where("ru.status IN (?)", bun.List([]string{RunPending, RunRunning})).Count(ctx)
	return n, mapError(err)
}

// ByIdempotencyKey returns the run of a report created with key since the given time.
func (r *RunRepo) ByIdempotencyKey(ctx context.Context, reportID uuid.UUID, key string, since time.Time) (*Run, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	run := new(Run)
	err = sc.NewSelect(run).Where("ru.report_id = ?", reportID).Where("ru.idempotency_key = ?", key).
		Where("ru.created_at >= ?", since.UTC()).OrderExpr("ru.id DESC").Limit(1).Scan(ctx)
	return run, mapError(err)
}

// CancelPending cancels a run that no worker has claimed yet. It reports false when the run is no
// longer pending.
func (r *RunRepo) CancelPending(ctx context.Context, id uuid.UUID, at time.Time, code string) (bool, error) {
	return r.transition(ctx, id, []string{RunPending}, map[string]any{
		"status": RunCancelled, "finished_at": at, "error_code": code,
	}, nil)
}

// RequestCancel asks the worker running a run to stop. It reports false when the run is not
// running.
func (r *RunRepo) RequestCancel(ctx context.Context, id uuid.UUID, at time.Time) (bool, error) {
	return r.transition(ctx, id, []string{RunRunning}, map[string]any{"cancel_requested_at": at}, nil)
}

// Release puts a running run back in the queue for another attempt.
func (r *RunRepo) Release(ctx context.Context, id uuid.UUID, instanceID string, attempt int, availableAt time.Time, code, message string) (bool, error) {
	return r.transition(ctx, id, []string{RunRunning}, map[string]any{
		"status": RunPending, "attempt": attempt, "available_at": availableAt, "instance_id": nil,
		"started_at": nil, "error_code": code, "error_message": message,
	}, &instanceID)
}

// Finish stores the outcome of a running run. It only succeeds while this instance still owns the
// run, so a run recovered by another instance is never overwritten by the one that lost it.
func (r *RunRepo) Finish(ctx context.Context, run *Run, instanceID string) (bool, error) {
	return r.transition(ctx, run.ID, []string{RunRunning}, map[string]any{
		"status": run.Status, "finished_at": run.FinishedAt, "duration_ms": run.DurationMS,
		"query_version_id": run.QueryVersionID, "resolved_params": nullJSON(run.ResolvedParams),
		"row_count": run.RowCount, "truncated": run.Truncated, "condition_result": nullJSON(run.ConditionResult),
		"result_hash": run.ResultHash, "result_sample": nullJSON(run.ResultSample),
		"error_code": run.ErrorCode, "error_message": run.ErrorMessage, "result_expires_at": run.ResultExpiresAt,
	}, &instanceID)
}

// FinishPending ends a run that was never claimed (for example skipped by the overlap policy).
func (r *RunRepo) FinishPending(ctx context.Context, id uuid.UUID, status, code string, at time.Time) (bool, error) {
	return r.transition(ctx, id, []string{RunPending}, map[string]any{
		"status": status, "finished_at": at, "error_code": code,
	}, nil)
}

// Recover marks a partial run as a success, once a resend delivered what had failed.
func (r *RunRepo) Recover(ctx context.Context, id uuid.UUID) (bool, error) {
	return r.transition(ctx, id, []string{RunPartial}, map[string]any{"status": RunSuccess, "error_code": nil}, nil)
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

// transition updates a run only while its status is one of from (and, with owner, while that
// instance owns it). Status transitions do not use the optimistic version: the guard is the status.
func (r *RunRepo) transition(ctx context.Context, id uuid.UUID, from []string, set map[string]any, owner *string) (bool, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return false, err
	}
	q := sc.NewUpdate((*Run)(nil)).Where("ru.id = ?", id).Where("ru.status IN (?)", bun.List(from)).
		Set("updated_at = ?", now()).Set("version = ru.version + 1")
	if owner != nil {
		q = q.Where("ru.instance_id = ?", *owner)
	}
	for col, v := range set {
		q = q.Set("? = ?", bun.Ident(col), v)
	}
	res, err := q.Exec(ctx)
	if err != nil {
		return false, mapError(err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// LatestWithResult returns a report's most recent run that kept a result sample, or ErrNotFound.
func (r *RunRepo) LatestWithResult(ctx context.Context, reportID uuid.UUID) (*Run, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	run := new(Run)
	err = sc.NewSelect(run).Where("ru.report_id = ?", reportID).Where("ru.result_sample IS NOT NULL").
		OrderExpr("ru.id DESC").Limit(1).Scan(ctx)
	return run, mapError(err)
}
