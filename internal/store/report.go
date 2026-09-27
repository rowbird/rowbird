package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Report policies (docs/spec/02-data-model.md).
const (
	MisfireRunOnce = "run_once"
	MisfireSkip    = "skip"
	OverlapSkip    = "skip"
	OverlapQueue   = "queue"

	PausedManual       = "manual"
	PausedAutoFailures = "auto_failures"
	// PausedGitOpsOrphan: the report's document left the GitOps directory.
	PausedGitOpsOrphan = "gitops_orphan"
)

// Report runs a query on a schedule (docs/spec/02-data-model.md).
type Report struct {
	bun.BaseModel `bun:"table:reports,alias:rp"`
	TenantBase
	Title       string `bun:"title,notnull"`
	Slug        string `bun:"slug,notnull"`
	Description string `bun:"description,notnull"`
	// ManagedBy is ManagedByGitOps for resources the configuration directory owns.
	ManagedBy string `bun:"managed_by,notnull"`
	// GitOpsOrphan marks a GitOps report whose document left the directory.
	GitOpsOrphan bool      `bun:"gitops_orphan,notnull"`
	QueryID      uuid.UUID `bun:"query_id,notnull,type:uuid"`
	Enabled      bool      `bun:"enabled,notnull"`
	Cron         string    `bun:"cron,notnull"`
	Timezone     string    `bun:"timezone,notnull"`
	// NextRunAt is null while the report is paused.
	NextRunAt *time.Time `bun:"next_run_at"`
	// Condition is the JSON of a condition.Spec.
	Condition           json.RawMessage   `bun:"condition,notnull,type:jsonb"`
	ParamOverrides      map[string]string `bun:"param_overrides,notnull"`
	MaxRows             *int              `bun:"max_rows"`
	RetryMax            int               `bun:"retry_max,notnull"`
	RetryBackoffSeconds int               `bun:"retry_backoff_seconds,notnull"`
	MisfirePolicy       string            `bun:"misfire_policy,notnull"`
	OverlapPolicy       string            `bun:"overlap_policy,notnull"`
	AutoPauseAfter      int               `bun:"auto_pause_after,notnull"`
	ConsecutiveFailures int               `bun:"consecutive_failures,notnull"`
	PausedReason        *string           `bun:"paused_reason"`
	OwnerID             *uuid.UUID        `bun:"owner_id,type:uuid"`
	NotifyOwnerOnFail   bool              `bun:"notify_owner_on_failure,notnull"`
	LastResultHash      *string           `bun:"last_result_hash"`
}

// ReportRepo persists reports, scoped to the workspace in ctx.
type ReportRepo struct{ s *Store }

// Reports returns the report repository.
func (s *Store) Reports() *ReportRepo { return &ReportRepo{s: s} }

// Create stores a report. A taken slug returns ErrDuplicate.
func (r *ReportRepo) Create(ctx context.Context, rp *Report) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	if rp.ParamOverrides == nil {
		rp.ParamOverrides = map[string]string{}
	}
	return sc.Insert(ctx, rp)
}

// Get returns a report by id.
func (r *ReportRepo) Get(ctx context.Context, id uuid.UUID) (*Report, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	rp := new(Report)
	err = sc.NewSelect(rp).Where("rp.id = ?", id).Scan(ctx)
	return rp, mapError(err)
}

// List returns every report, by title.
func (r *ReportRepo) List(ctx context.Context) ([]Report, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Report
	err = sc.NewSelect(&out).OrderExpr("rp.title, rp.id").Scan(ctx)
	return out, mapError(err)
}

// ListByQuery returns the reports that run a query.
func (r *ReportRepo) ListByQuery(ctx context.Context, queryID uuid.UUID) ([]Report, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Report
	err = sc.NewSelect(&out).Where("rp.query_id = ?", queryID).OrderExpr("rp.title").Scan(ctx)
	return out, mapError(err)
}

// CountByQuery returns the number of reports per query id.
func (r *ReportRepo) CountByQuery(ctx context.Context) (map[uuid.UUID]int, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		QueryID uuid.UUID `bun:"query_id,type:uuid"`
		N       int       `bun:"n"`
	}
	err = sc.NewSelect((*Report)(nil)).ColumnExpr("rp.query_id, COUNT(*) AS n").GroupExpr("rp.query_id").Scan(ctx, &rows)
	if err != nil {
		return nil, mapError(err)
	}
	out := make(map[uuid.UUID]int, len(rows))
	for _, row := range rows {
		out[row.QueryID] = row.N
	}
	return out, nil
}

// Update saves columns with optimistic concurrency. Used for changes a user makes.
func (r *ReportRepo) Update(ctx context.Context, rp *Report, columns ...string) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	return sc.Update(ctx, rp, columns...)
}

// SetNextRun moves the schedule forward. It is a system change and does not bump the version, so
// it never conflicts with a user editing the report.
func (r *ReportRepo) SetNextRun(ctx context.Context, id uuid.UUID, next *time.Time) error {
	return r.system(ctx, id, map[string]any{"next_run_at": next})
}

// RecordOutcome stores what a finished run changes on its report: the failure streak, the last
// delivered result and, when the streak reaches the limit, the automatic pause. Like SetNextRun it
// does not bump the version.
func (r *ReportRepo) RecordOutcome(ctx context.Context, id uuid.UUID, failures int, lastHash *string, autoPause bool) error {
	set := map[string]any{"consecutive_failures": failures}
	if lastHash != nil {
		set["last_result_hash"] = *lastHash
	}
	if autoPause {
		set["enabled"] = false
		set["paused_reason"] = PausedAutoFailures
		set["next_run_at"] = nil
	}
	return r.system(ctx, id, set)
}

func (r *ReportRepo) system(ctx context.Context, id uuid.UUID, set map[string]any) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	q := sc.NewUpdate((*Report)(nil)).Where("rp.id = ?", id)
	for col, v := range set {
		q = q.Set("? = ?", bun.Ident(col), v)
	}
	res, err := q.Exec(ctx)
	if err != nil {
		return mapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a report and, by cascade, its runs.
func (r *ReportRepo) Delete(ctx context.Context, id uuid.UUID) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	if _, err := sc.NewDelete((*Run)(nil)).Where("ru.report_id = ?", id).Exec(ctx); err != nil {
		return mapError(err)
	}
	res, err := sc.NewDelete((*Report)(nil)).Where("rp.id = ?", id).Exec(ctx)
	if err != nil {
		return mapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetBySlug returns the report with that slug, or ErrNotFound.
func (r *ReportRepo) GetBySlug(ctx context.Context, slug string) (*Report, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	v := new(Report)
	err = sc.NewSelect(v).Where("rp.slug = ?", slug).Scan(ctx)
	return v, mapError(err)
}
