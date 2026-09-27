package reports_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/condition"
	"github.com/rowbird/rowbird/internal/params"
	"github.com/rowbird/rowbird/internal/reports"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/testenv"
)

type env struct {
	*testenv.Env
	svc      *reports.Service
	query    uuid.UUID
	woken    int
	canceled []uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{Env: testenv.New(t)}
	def := "50"
	q := e.Query(t, "big-orders", "select id, total from orders where total >= {{min_total}} and created_at < {{today}}",
		params.Definition{Name: "min_total", Type: params.Decimal, Default: &def})
	e.query = q.Query.ID
	e.svc = reports.NewService(e.Store, e.Queries, reports.Options{
		Now: e.Clock.Now, Wake: func() { e.woken++ }, CancelLocal: func(id uuid.UUID) { e.canceled = append(e.canceled, id) },
	})
	return e
}

func fields(t *testing.T, err error) map[string]string {
	t.Helper()
	ae, ok := apperr.As(err)
	if !ok || ae.Kind != apperr.KindInvalid {
		t.Fatalf("got %v, want a validation error", err)
	}
	out := map[string]string{}
	for _, f := range ae.Fields {
		out[f.Field] = f.Code
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func TestCreateDefaultsAndSchedule(t *testing.T) {
	e := newEnv(t)
	v, err := e.svc.Create(e.Ctx, e.Principal, reports.Input{Title: "Relatório diário", QueryID: e.query, Cron: "0 7 * * 1-5"})
	if err != nil {
		t.Fatal(err)
	}
	rp := v.Report
	// Friday 2026-09-25 09:00 in São Paulo: the next weekday 07:00 is Monday the 28th (10:00 UTC).
	if rp.Slug != "relatorio-diario" || rp.Timezone != testenv.Timezone || !rp.Enabled || rp.NextRunAt == nil ||
		!rp.NextRunAt.Equal(time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("report %+v next %v", rp, rp.NextRunAt)
	}
	if rp.RetryMax != 2 || rp.RetryBackoffSeconds != 30 || rp.AutoPauseAfter != 5 || rp.MisfirePolicy != store.MisfireRunOnce ||
		rp.OverlapPolicy != store.OverlapSkip || !rp.NotifyOwnerOnFail || *rp.OwnerID != e.Principal.UserID {
		t.Fatalf("defaults %+v", rp)
	}
	if v.Status != reports.StatusActive || v.QueryTitle != "big-orders" || !reflect.DeepEqual(v.Condition, condition.Always()) {
		t.Fatalf("view %+v", v)
	}
	if _, err := e.svc.Create(e.Ctx, e.Principal, reports.Input{Title: "Other", Slug: "relatorio-diario", QueryID: e.query, Cron: "@daily"}); !errors.Is(err, reports.ErrSlugTaken) {
		t.Fatalf("slug taken: %v", err)
	}
	disabled, err := e.svc.Create(e.Ctx, e.Principal, reports.Input{Title: "Off", QueryID: e.query, Cron: "@daily", Enabled: ptr(false)})
	if err != nil || disabled.Report.NextRunAt != nil || disabled.Status != reports.StatusPaused {
		t.Fatalf("disabled %+v %v", disabled, err)
	}
	q, _ := e.Queries.Get(e.Ctx, e.query)
	if q.ReportCount != 2 {
		t.Errorf("report count %d", q.ReportCount)
	}
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Create(e.Ctx, e.Principal, reports.Input{
		Title: "", Slug: "Bad Slug", QueryID: uuid.New(), Cron: "* * *", Timezone: "Nowhere/City",
		MaxRows: ptr(0), RetryMax: ptr(11), RetryBackoffSeconds: ptr(0), AutoPauseAfter: ptr(-1),
		MisfirePolicy: "later", OverlapPolicy: "both",
		Condition: condition.Spec{Match: "most"},
	})
	want := map[string]string{
		"title": "validation.required", "slug": "validation.slug", "query_id": "validation.invalid_value",
		"cron": "validation.cron", "max_rows": "validation.range", "retry_max": "validation.range",
		"retry_backoff_seconds": "validation.range", "auto_pause_after": "validation.range",
		"misfire_policy": "validation.invalid_value", "overlap_policy": "validation.invalid_value",
		"condition.match": "validation.invalid_value",
	}
	// An invalid time zone is reported before the expression is parsed.
	got := fields(t, err)
	delete(want, "cron")
	want["timezone"] = "validation.timezone"
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields %v\nwant %v", got, want)
	}

	_, err = e.svc.Create(e.Ctx, e.Principal, reports.Input{
		Title: "x", QueryID: e.query, Cron: "0 0 30 2 *",
		ParamOverrides: map[string]string{"min_total": "lots", "nope": "1"},
		Condition:      condition.Spec{Rules: []condition.Rule{{Type: "row_count", Params: map[string]any{"op": "gt", "value": "x"}}}},
	})
	got = fields(t, err)
	want = map[string]string{"cron": "validation.cron_never", "param_overrides.nope": params.CodeUndefined, "condition.rules[0].value": "validation.invalid_type"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields %v\nwant %v", got, want)
	}
	_, err = e.svc.Create(e.Ctx, e.Principal, reports.Input{Title: "x", QueryID: e.query, Cron: "@hourly", ParamOverrides: map[string]string{"min_total": "lots"}})
	if got := fields(t, err); got["param_overrides.min_total"] != params.CodeInvalidValue {
		t.Errorf("fields %v", got)
	}

	// A parameter without default needs an override.
	q := e.Query(t, "by-region", "select * from orders where region = {{region}}", params.Definition{Name: "region", Type: params.Text})
	_, err = e.svc.Create(e.Ctx, e.Principal, reports.Input{Title: "x", QueryID: q.Query.ID, Cron: "@hourly"})
	if got := fields(t, err); got["param_overrides.region"] != params.CodeRequired {
		t.Errorf("fields %v", got)
	}
	if _, err := e.svc.Create(e.Ctx, e.Principal, reports.Input{Title: "x", QueryID: q.Query.ID, Cron: "@hourly", ParamOverrides: map[string]string{"region": "south"}}); err != nil {
		t.Errorf("with override: %v", err)
	}
}

func TestUpdatePauseResumeDelete(t *testing.T) {
	e := newEnv(t)
	v, err := e.svc.Create(e.Ctx, e.Principal, reports.Input{
		Title: "R", QueryID: e.query, Cron: "0 * * * *", Timezone: "UTC",
		Condition: condition.Spec{Match: "any", Rules: []condition.Rule{{Type: "has_rows"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Report.NextRunAt.Equal(time.Date(2026, 9, 25, 13, 0, 0, 0, time.UTC)) || v.Condition.Match != "any" || len(v.Condition.Rules) != 1 {
		t.Fatalf("created %+v", v)
	}
	// A title change keeps the schedule; a cron change recomputes it from now.
	e.Clock.Advance(90 * time.Minute)
	v, err = e.svc.Update(e.Ctx, v.Report.ID, reports.Patch{Version: v.Report.Version, Title: ptr("Hourly")})
	if err != nil || v.Report.Title != "Hourly" || !v.Report.NextRunAt.Equal(time.Date(2026, 9, 25, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("title update %+v %v", v, err)
	}
	v, err = e.svc.Update(e.Ctx, v.Report.ID, reports.Patch{Version: v.Report.Version, Cron: ptr("*/15 * * * *"), MaxRows: ptr(10)})
	if err != nil || !v.Report.NextRunAt.Equal(time.Date(2026, 9, 25, 13, 45, 0, 0, time.UTC)) || *v.Report.MaxRows != 10 {
		t.Fatalf("cron update %+v %v", v.Report, err)
	}
	v, err = e.svc.Update(e.Ctx, v.Report.ID, reports.Patch{Version: v.Report.Version, ClearMaxRows: true, Condition: &condition.Spec{}})
	if err != nil || v.Report.MaxRows != nil || len(v.Condition.Rules) != 0 || v.Condition.Match != "all" {
		t.Fatalf("clear %+v %v", v.Report, err)
	}
	if _, err := e.svc.Update(e.Ctx, v.Report.ID, reports.Patch{Version: 1, Title: ptr("Stale")}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}

	v, err = e.svc.Pause(e.Ctx, v.Report.ID)
	if err != nil || v.Report.Enabled || v.Report.NextRunAt != nil || *v.Report.PausedReason != store.PausedManual || v.Status != reports.StatusPaused {
		t.Fatalf("pause %+v %v", v.Report, err)
	}
	_ = e.Store.Reports().RecordOutcome(e.Ctx, v.Report.ID, 3, nil, false)
	e.Clock.Advance(time.Hour)
	v, err = e.svc.Resume(e.Ctx, v.Report.ID)
	if err != nil || !v.Report.Enabled || v.Report.PausedReason != nil || v.Report.ConsecutiveFailures != 0 ||
		!v.Report.NextRunAt.Equal(time.Date(2026, 9, 25, 14, 45, 0, 0, time.UTC)) {
		t.Fatalf("resume %+v %v", v.Report, err)
	}

	// The query cannot be deleted while the report uses it.
	err = e.Queries.Delete(e.Ctx, e.query)
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "query.in_use" || len(ae.Dependents) != 1 || ae.Dependents[0].Type != "report" || ae.Dependents[0].Name != "Hourly" {
		t.Fatalf("query delete: %v", err)
	}

	run, _, err := e.svc.Run(e.Ctx, e.Principal, v.Report.ID, reports.RunInput{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(e.Ctx, v.Report.ID); !errors.Is(err, reports.ErrRunActive) {
		t.Fatalf("delete with active run: %v", err)
	}
	if _, err := e.svc.CancelRun(e.Ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(e.Ctx, v.Report.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.Queries.Delete(e.Ctx, e.query); err != nil {
		t.Fatalf("query delete after the report: %v", err)
	}
}

func TestManualRunsAndCancel(t *testing.T) {
	e := newEnv(t)
	v, err := e.svc.Create(e.Ctx, e.Principal, reports.Input{Title: "R", QueryID: e.query, Cron: "@daily", Enabled: ptr(false)})
	if err != nil {
		t.Fatal(err)
	}
	id := v.Report.ID
	run, created, err := e.svc.Run(e.Ctx, e.Principal, id, reports.RunInput{Deliver: true, IdempotencyKey: "abc"})
	if err != nil || !created || run.Trigger != store.TriggerManual || !run.Deliver || run.Status != store.RunPending || *run.TriggeredBy != e.Principal.UserID || e.woken != 1 {
		t.Fatalf("run %+v created=%v woken=%d %v", run, created, e.woken, err)
	}
	again, created, err := e.svc.Run(e.Ctx, e.Principal, id, reports.RunInput{Deliver: true, IdempotencyKey: "abc"})
	if err != nil || created || again.ID != run.ID || e.woken != 1 {
		t.Fatalf("idempotent run %+v %v", again, err)
	}
	e.Clock.Advance(25 * time.Hour)
	later, created, _ := e.svc.Run(e.Ctx, e.Principal, id, reports.RunInput{IdempotencyKey: "abc"})
	if !created || later.ID == run.ID {
		t.Fatal("an expired idempotency key returned the old run")
	}
	if _, _, err := e.svc.Run(e.Ctx, e.Principal, uuid.New(), reports.RunInput{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown report: %v", err)
	}

	// Pending runs are cancelled at once; running ones are asked to stop.
	rv, err := e.svc.CancelRun(e.Ctx, run.ID)
	if err != nil || rv.Run.Status != store.RunCancelled || *rv.Run.ErrorCode != reports.CodeCancelled {
		t.Fatalf("cancel pending %+v %v", rv, err)
	}
	if _, err := e.svc.CancelRun(e.Ctx, run.ID); !errors.Is(err, reports.ErrRunNotActive) {
		t.Fatalf("cancel twice: %v", err)
	}
	if ok, _ := e.Store.System().Claim(e.Ctx, later.ID, "me", e.Clock.Now()); !ok {
		t.Fatal("claim")
	}
	rv, err = e.svc.CancelRun(e.Ctx, later.ID)
	if err != nil || rv.Run.Status != store.RunRunning || rv.Run.CancelRequestedAt == nil || len(e.canceled) != 1 || e.canceled[0] != later.ID {
		t.Fatalf("cancel running %+v %v %v", rv, err, e.canceled)
	}

	page, err := e.svc.ListRuns(e.Ctx, store.RunFilter{ReportID: id}, store.PageRequest{})
	if err != nil || len(page.Items) != 2 || page.Items[0].ReportTitle != "R" || page.Items[0].TriggeredByName != "Ana" {
		t.Fatalf("runs %+v %v", page, err)
	}
	list, _ := e.svc.List(e.Ctx)
	if len(list) != 1 || list[0].LastRun == nil || list[0].LastRun.ID != later.ID {
		t.Fatalf("list %+v", list)
	}
}

func TestPreviewSchedule(t *testing.T) {
	e := newEnv(t)
	sv, err := e.svc.PreviewSchedule(e.Ctx, "0 7 * * 1-5", "", 3, "pt-BR")
	if err != nil {
		t.Fatal(err)
	}
	if sv.Timezone != testenv.Timezone || sv.Description != "Às 07:00, de segunda-feira a sexta-feira" || len(sv.Next) != 3 ||
		!sv.Next[0].Equal(time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("preview %+v", sv)
	}
	_, err = e.svc.PreviewSchedule(e.Ctx, "@every 5m", "UTC", 50, "en")
	if got := fields(t, err); got["cron"] != "validation.cron" || got["count"] != "validation.range" {
		t.Fatalf("fields %v", got)
	}
}
