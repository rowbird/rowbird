package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/condition"
	"github.com/rowbird/rowbird/internal/connections"
	"github.com/rowbird/rowbird/internal/delivery"
	_ "github.com/rowbird/rowbird/internal/format/csv"
	_ "github.com/rowbird/rowbird/internal/format/inline"
	_ "github.com/rowbird/rowbird/internal/format/xlsx"
	"github.com/rowbird/rowbird/internal/params"
	"github.com/rowbird/rowbird/internal/reports"
	"github.com/rowbird/rowbird/internal/runner"
	"github.com/rowbird/rowbird/internal/scheduler"
	localstorage "github.com/rowbird/rowbird/internal/storage/local"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/testenv"
)

type harness struct {
	*testenv.Env
	reports  *reports.Service
	sched    *scheduler.Scheduler
	run      *runner.Runner
	spool    string
	fake     uuid.UUID
	channels *channels.Service
	storage  *localstorage.Storage
	engine   *delivery.Engine
	notified *recorder
}

// recorder is a runner.Notifier that remembers what it was told.
type recorder struct {
	mu       sync.Mutex
	updates  []string
	finished []string
}

func (r *recorder) RunUpdated(_ context.Context, run *store.Run) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, run.Status)
}

func (r *recorder) RunFinished(_ context.Context, run *store.Run, _ *store.Report, paused bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished = append(r.finished, fmt.Sprintf("%s paused=%t", run.Status, paused))
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{Env: testenv.New(t), spool: t.TempDir(), notified: &recorder{}}
	h.channels = channels.NewService(h.Store, h.Keyring, channels.Options{})
	h.storage, _ = localstorage.New(t.TempDir())
	h.engine = delivery.New(h.Store, h.channels, delivery.Options{
		Storage: h.storage, Backend: "local", BaseURL: "https://rb.example.com", SpoolDir: h.spool, Now: h.Clock.Now, Backoff: time.Millisecond,
	})
	h.run = h.newRunner("main")
	h.reports = reports.NewService(h.Store, h.Queries, reports.Options{
		Now: h.Clock.Now, Wake: h.run.Wake, CancelLocal: h.run.Cancel, SpoolDir: h.spool, BaseURL: "https://rb.example.com", Deliverer: h.engine, Storage: h.storage,
		Background: func(f func()) { f() },
	})
	h.sched = scheduler.New(h.Store, scheduler.Options{Now: h.Clock.Now, Wake: h.run.Wake})
	fake, err := h.Conns.Create(h.Ctx, h.Principal, connections.Input{Name: "fake", Driver: "fakerun", Config: map[string]any{"password": fakePassword}}, auth.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	h.fake = fake.ID
	return h
}

func (h *harness) newRunner(instance string) *runner.Runner {
	return runner.New(runner.Config{Workers: 2, Tick: time.Second, SpoolDir: h.spool, InstanceID: instance, Hostname: "test", Version: "dev"},
		h.Store, h.Queries, h.Conns, runner.Options{Now: h.Clock.Now, Delivery: h.engine, Notifier: h.notified})
}

// report saves a query (on the shop database, or on the fake one when sql mentions a fake verb)
// and a report on it.
func (h *harness) report(t *testing.T, sql string, in reports.Input) *store.Report {
	t.Helper()
	conn := h.Conn.ID
	for _, verb := range []string{"block", "transient", "auth", "leak"} {
		if strings.Contains(sql, verb) {
			conn = h.fake
		}
	}
	slug := "q-" + uuid.NewString()[:8]
	q, err := h.Queries.Create(h.Ctx, h.Principal, queriesInput(slug, conn, sql))
	if err != nil {
		t.Fatal(err)
	}
	in.QueryID = q.Query.ID
	if in.Title == "" {
		in.Title = slug
	}
	if in.Cron == "" {
		in.Cron = "*/5 * * * *"
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	v, err := h.reports.Create(h.Ctx, h.Principal, in)
	if err != nil {
		t.Fatal(err)
	}
	return &v.Report
}

func (h *harness) get(t *testing.T, id uuid.UUID) *store.Report {
	t.Helper()
	rp, err := h.Store.Reports().Get(h.Ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return rp
}

// runs returns a report's runs, oldest first, with their full rows.
func (h *harness) runs(t *testing.T, reportID uuid.UUID) []store.Run {
	t.Helper()
	page, err := h.Store.Runs().List(h.Ctx, store.RunFilter{ReportID: reportID}, store.PageRequest{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]store.Run, len(page.Items))
	for i, r := range page.Items {
		full, err := h.Store.Runs().Get(h.Ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[len(out)-1-i] = *full
	}
	return out
}

func (h *harness) tick(t *testing.T) int {
	t.Helper()
	n, err := h.sched.Tick(h.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (h *harness) process(t *testing.T, r *runner.Runner) bool {
	t.Helper()
	ok, err := r.ProcessNext(h.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func at(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t.UTC()
}

func ptr[T any](v T) *T { return &v }

func code(r store.Run) string {
	if r.ErrorCode == nil {
		return ""
	}
	return *r.ErrorCode
}

func TestScheduledRun(t *testing.T) {
	h := newHarness(t)
	def := "50"
	q, err := h.Queries.Create(h.Ctx, h.Principal, queriesInputParams("big", h.Conn.ID,
		"select id, total, created_at from orders where total >= {{min_total}} and created_at < {{today}} order by id",
		params.Definition{Name: "min_total", Type: params.Decimal, Default: &def}))
	if err != nil {
		t.Fatal(err)
	}
	v, err := h.reports.Create(h.Ctx, h.Principal, reports.Input{Title: "Big", QueryID: q.Query.ID, Cron: "*/5 * * * *", Timezone: "America/Sao_Paulo"})
	if err != nil {
		t.Fatal(err)
	}
	rp := v.Report
	if !rp.NextRunAt.Equal(at("2026-09-25T12:05:00Z")) {
		t.Fatalf("next %v", rp.NextRunAt)
	}
	if n := h.tick(t); n != 0 {
		t.Fatalf("queued %d runs before the slot", n)
	}
	h.Clock.Set(at("2026-09-25T12:05:03Z"))
	if n := h.tick(t); n != 1 {
		t.Fatalf("queued %d runs", n)
	}
	if n := h.tick(t); n != 0 {
		t.Fatalf("queued the same slot twice (%d)", n)
	}
	if !h.get(t, rp.ID).NextRunAt.Equal(at("2026-09-25T12:10:00Z")) {
		t.Fatalf("schedule did not advance: %v", h.get(t, rp.ID).NextRunAt)
	}
	if !h.process(t, h.run) {
		t.Fatal("no run to process")
	}
	if h.process(t, h.run) {
		t.Fatal("processed a second run")
	}
	runs := h.runs(t, rp.ID)
	if len(runs) != 1 {
		t.Fatalf("%d runs", len(runs))
	}
	r := runs[0]
	if r.Status != store.RunSuccess || r.Trigger != store.TriggerSchedule || !r.ScheduledFor.Equal(at("2026-09-25T12:05:00Z")) ||
		*r.RowCount != 1 || r.Truncated || r.ResultHash == nil || *r.InstanceID != "main" || r.QueryVersionID == nil || r.FinishedAt == nil {
		t.Fatalf("run %+v", r)
	}
	var sample runner.Sample
	if err := json.Unmarshal(r.ResultSample, &sample); err != nil || len(sample.Columns) != 3 || len(sample.Rows) != 1 {
		t.Fatalf("sample %s %v", r.ResultSample, err)
	}
	if sample.Rows[0][0] != float64(1) || sample.Rows[0][1] != "120.5" {
		t.Errorf("sample row %v", sample.Rows[0])
	}
	var rparams runner.ResolvedParams
	_ = json.Unmarshal(r.ResolvedParams, &rparams)
	if rparams.Timezone != "America/Sao_Paulo" || len(rparams.Values) != 2 || rparams.Values[1].Name != "today" || rparams.Values[1].Value != "2026-09-25" {
		t.Errorf("params %+v", rparams)
	}
	var cond condition.Result
	_ = json.Unmarshal(r.ConditionResult, &cond)
	if !cond.Passed {
		t.Errorf("condition %+v", cond)
	}
	after := h.get(t, rp.ID)
	if after.LastResultHash == nil || *after.LastResultHash != *r.ResultHash || after.ConsecutiveFailures != 0 {
		t.Errorf("report after run %+v", after)
	}
	// The result stays downloadable for a day.
	if r.ResultExpiresAt == nil || !r.ResultExpiresAt.Equal(at("2026-09-26T12:05:03Z")) {
		t.Errorf("result expires at %v", r.ResultExpiresAt)
	}
	if _, err := os.Stat(runner.SpoolPath(h.spool, r.ID)); err != nil {
		t.Errorf("spool of a successful run: %v", err)
	}
	if entries, _ := os.ReadDir(h.spool); len(entries) != 1 {
		t.Errorf("spool files: %v", entries)
	}
}

func TestMisfire(t *testing.T) {
	for _, policy := range []string{store.MisfireRunOnce, store.MisfireSkip} {
		t.Run(policy, func(t *testing.T) {
			h := newHarness(t)
			rp := h.report(t, "select 1", reports.Input{MisfirePolicy: policy})
			// The server was down for an hour: twelve slots were missed.
			h.Clock.Set(at("2026-09-25T13:02:00Z"))
			queued := h.tick(t)
			runs := h.runs(t, rp.ID)
			if policy == store.MisfireRunOnce {
				if queued != 1 || len(runs) != 1 || !runs[0].ScheduledFor.Equal(at("2026-09-25T12:05:00Z")) {
					t.Fatalf("catch-up runs %d %+v", queued, runs)
				}
			} else if queued != 0 || len(runs) != 0 {
				t.Fatalf("skip policy queued %d", queued)
			}
			if next := h.get(t, rp.ID).NextRunAt; !next.Equal(at("2026-09-25T13:05:00Z")) {
				t.Fatalf("next %v", next)
			}
			// A slot a little late (within the grace period) is not a misfire.
			h.Clock.Set(at("2026-09-25T13:06:30Z"))
			if h.tick(t) != 1 {
				t.Fatal("slot within grace was not queued")
			}
		})
	}
}

func TestConditionAndChanged(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select region from orders order by id", reports.Input{
		Condition: condition.Spec{Match: "all", Rules: []condition.Rule{{Type: "changed"}}},
	})
	h.Clock.Set(at("2026-09-25T12:05:00Z"))
	h.tick(t)
	h.process(t, h.run)
	h.Clock.Set(at("2026-09-25T12:10:00Z"))
	h.tick(t)
	h.process(t, h.run)
	// A manual run without delivery does not count and does not move the last result.
	_, _, _ = h.reports.Run(h.Ctx, h.Principal, rp.ID, reports.RunInput{})
	h.process(t, h.run)
	runs := h.runs(t, rp.ID)
	if len(runs) != 3 || runs[0].Status != store.RunSuccess || runs[1].Status != store.RunSkipped || code(runs[1]) != runner.CodeConditionFalse ||
		runs[2].Status != store.RunSkipped || runs[2].Trigger != store.TriggerManual {
		t.Fatalf("runs %v %v %v", runs[0].Status, runs[1].Status, runs[2].Status)
	}
	var cond condition.Result
	_ = json.Unmarshal(runs[0].ConditionResult, &cond)
	if !cond.Passed || cond.Rules[0].Detail["first_result"] != true {
		t.Errorf("first condition %+v", cond)
	}
}

func TestRetriesAndAutoPause(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select 1 -- transient", reports.Input{RetryMax: ptr(1), RetryBackoffSeconds: ptr(10), AutoPauseAfter: ptr(2)})
	h.Clock.Set(at("2026-09-25T12:05:00Z"))
	h.tick(t)
	h.process(t, h.run)
	runs := h.runs(t, rp.ID)
	if runs[0].Status != store.RunPending || runs[0].Attempt != 2 || code(runs[0]) != "query.timeout" || !runs[0].AvailableAt.Equal(at("2026-09-25T12:05:10Z")) {
		t.Fatalf("after first attempt %+v", runs[0])
	}
	if h.process(t, h.run) {
		t.Fatal("retry claimed before its backoff")
	}
	h.Clock.Set(at("2026-09-25T12:05:10Z"))
	h.process(t, h.run)
	runs = h.runs(t, rp.ID)
	if runs[0].Status != store.RunFailed || runs[0].Attempt != 2 || !strings.Contains(*runs[0].ErrorMessage, "statement timeout") {
		t.Fatalf("after retries %+v", runs[0])
	}
	if got := h.get(t, rp.ID); got.ConsecutiveFailures != 1 || !got.Enabled {
		t.Fatalf("report after one failure %+v", got)
	}
	// The second failed run pauses the report.
	h.Clock.Set(at("2026-09-25T12:10:00Z"))
	h.tick(t)
	h.process(t, h.run)
	h.Clock.Set(at("2026-09-25T12:10:10Z"))
	h.process(t, h.run)
	got := h.get(t, rp.ID)
	if got.ConsecutiveFailures != 2 || got.Enabled || got.NextRunAt != nil || *got.PausedReason != store.PausedAutoFailures {
		t.Fatalf("report after two failures %+v", got)
	}
	h.Clock.Set(at("2026-09-25T13:00:00Z"))
	if h.tick(t) != 0 {
		t.Fatal("a paused report was scheduled")
	}
	d, err := h.reports.Dashboard(h.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.FailingReports) != 1 || d.FailingReports[0].ID != rp.ID || len(d.RecentFailures) != 2 || d.RecentFailures[0].ReportTitle == "" ||
		d.Rate7 != (reports.SuccessRate{Total: 2}) || len(d.NextRuns) != 0 {
		t.Errorf("dashboard %+v", d)
	}
	wantUpdates := []string{"running", "pending", "running", "failed", "running", "pending", "running", "failed"}
	if fmt.Sprint(h.notified.updates) != fmt.Sprint(wantUpdates) {
		t.Errorf("updates %v", h.notified.updates)
	}
	if want := []string{"failed paused=false", "failed paused=true"}; fmt.Sprint(h.notified.finished) != fmt.Sprint(want) {
		t.Errorf("finished %v", h.notified.finished)
	}
}

func TestPermanentFailures(t *testing.T) {
	h := newHarness(t)
	auth := h.report(t, "select 1 -- auth", reports.Input{})
	leak := h.report(t, "select 1 -- leak", reports.Input{})
	for _, id := range []uuid.UUID{auth.ID, leak.ID} {
		if _, _, err := h.reports.Run(h.Ctx, h.Principal, id, reports.RunInput{Deliver: true}); err != nil {
			t.Fatal(err)
		}
		h.process(t, h.run)
	}
	a := h.runs(t, auth.ID)[0]
	if a.Status != store.RunFailed || a.Attempt != 1 || code(a) != "connection.auth_failed" {
		t.Fatalf("auth failure %+v", a)
	}
	if h.get(t, auth.ID).ConsecutiveFailures != 1 {
		t.Error("a manual run with delivery did not count")
	}
	// query.failed is retried; its message never carries the connection's secrets.
	l := h.runs(t, leak.ID)[0]
	if l.Status != store.RunPending || strings.Contains(*l.ErrorMessage, fakePassword) || !strings.Contains(*l.ErrorMessage, "syntax error") {
		t.Fatalf("leak %+v %q", l, *l.ErrorMessage)
	}

	// A parameter the query needs but the report no longer supplies.
	q := h.Query(t, "by-region", "select * from orders where region = {{region}}", params.Definition{Name: "region", Type: params.Text})
	v, err := h.reports.Create(h.Ctx, h.Principal, reports.Input{Title: "Region", QueryID: q.Query.ID, Cron: "@daily", ParamOverrides: map[string]string{"region": "south"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = h.Store.Reports().Update(h.Ctx, &store.Report{TenantBase: v.Report.TenantBase, ParamOverrides: map[string]string{}}, "param_overrides")
	_, _, _ = h.reports.Run(h.Ctx, h.Principal, v.Report.ID, reports.RunInput{})
	h.process(t, h.run)
	p := h.runs(t, v.Report.ID)[0]
	if p.Status != store.RunFailed || code(p) != runner.CodeInvalidParams || *p.ErrorMessage != "region: validation.required" {
		t.Fatalf("params %+v", p)
	}
}

// startBlocking processes one run in the background; the run's query blocks until cancelled.
func startBlocking(t *testing.T, h *harness, r *runner.Runner) chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := r.ProcessNext(h.Ctx); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-fakeStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("the blocking query did not start")
	}
	return done
}

func wait(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not stop")
	}
}

func TestOverlapAndCancel(t *testing.T) {
	for _, policy := range []string{store.OverlapSkip, store.OverlapQueue} {
		t.Run(policy, func(t *testing.T) {
			h := newHarness(t)
			rp := h.report(t, "select 1 -- block", reports.Input{OverlapPolicy: policy})
			h.Clock.Set(at("2026-09-25T12:05:00Z"))
			h.tick(t)
			done := startBlocking(t, h, h.run)
			h.Clock.Set(at("2026-09-25T12:10:00Z"))
			h.tick(t)
			if h.process(t, h.run) {
				t.Fatal("a second scheduled run started while the first was running")
			}
			runs := h.runs(t, rp.ID)
			if policy == store.OverlapSkip && (runs[1].Status != store.RunSkipped || code(runs[1]) != runner.CodeOverlap) {
				t.Fatalf("skip policy %+v", runs[1])
			}
			if policy == store.OverlapQueue && runs[1].Status != store.RunPending {
				t.Fatalf("queue policy %+v", runs[1])
			}
			if _, err := h.reports.CancelRun(h.Ctx, runs[0].ID); err != nil {
				t.Fatal(err)
			}
			wait(t, done)
			runs = h.runs(t, rp.ID)
			if runs[0].Status != store.RunCancelled || code(runs[0]) != runner.CodeCancelled || runs[0].FinishedAt == nil {
				t.Fatalf("cancelled run %+v", runs[0])
			}
			if h.get(t, rp.ID).ConsecutiveFailures != 0 {
				t.Error("a cancelled run counted as a failure")
			}
			if policy == store.OverlapQueue {
				done = startBlocking(t, h, h.run)
				h.run.Cancel(runs[1].ID)
				wait(t, done)
			}
		})
	}
}

func TestManualRunIgnoresOverlap(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select 1 -- block", reports.Input{})
	_, _, _ = h.reports.Run(h.Ctx, h.Principal, rp.ID, reports.RunInput{})
	first := startBlocking(t, h, h.run)
	_, _, _ = h.reports.Run(h.Ctx, h.Principal, rp.ID, reports.RunInput{})
	second := startBlocking(t, h, h.run)
	for _, r := range h.runs(t, rp.ID) {
		if r.Status != store.RunRunning {
			t.Errorf("run %s is %s", r.ID, r.Status)
		}
		h.run.Cancel(r.ID)
	}
	wait(t, first)
	wait(t, second)
}

func TestCancelFromAnotherInstance(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select 1 -- block", reports.Input{})
	other := h.newRunner("other")
	_, _, _ = h.reports.Run(h.Ctx, h.Principal, rp.ID, reports.RunInput{})
	done := startBlocking(t, h, other)
	run := h.runs(t, rp.ID)[0]
	// The API runs on this instance: the local cancel finds nothing, the request waits in the store.
	if _, err := h.reports.CancelRun(h.Ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := other.Maintain(h.Ctx); err != nil {
		t.Fatal(err)
	}
	wait(t, done)
	if got := h.runs(t, rp.ID)[0]; got.Status != store.RunCancelled {
		t.Fatalf("run %+v", got)
	}
}

func TestInstanceLost(t *testing.T) {
	h := newHarness(t)
	retried := h.report(t, "select 1", reports.Input{RetryMax: ptr(1)})
	failed := h.report(t, "select 1", reports.Input{RetryMax: ptr(0)})
	sys := h.Store.System()
	dead := &store.Instance{ID: "dead", Hostname: "h", Version: "dev"}
	if err := sys.Heartbeat(h.Ctx, dead, h.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{retried.ID, failed.ID} {
		run, _, _ := h.reports.Run(h.Ctx, h.Principal, id, reports.RunInput{Deliver: true})
		if ok, _ := sys.Claim(h.Ctx, run.ID, "dead", h.Clock.Now()); !ok {
			t.Fatal("claim")
		}
	}
	// Still fresh: nothing is recovered.
	h.Clock.Advance(10 * time.Second)
	if err := h.run.Maintain(h.Ctx); err != nil {
		t.Fatal(err)
	}
	if h.runs(t, retried.ID)[0].Status != store.RunRunning {
		t.Fatal("recovered a run of a live instance")
	}
	h.Clock.Advance(time.Minute)
	if err := h.run.Maintain(h.Ctx); err != nil {
		t.Fatal(err)
	}
	r := h.runs(t, retried.ID)[0]
	if r.Status != store.RunPending || r.Attempt != 2 || code(r) != runner.CodeInstanceLost {
		t.Fatalf("retried %+v", r)
	}
	f := h.runs(t, failed.ID)[0]
	if f.Status != store.RunFailed || code(f) != runner.CodeInstanceLost || h.get(t, failed.ID).ConsecutiveFailures != 1 {
		t.Fatalf("failed %+v", f)
	}
	h.process(t, h.run)
	if r := h.runs(t, retried.ID)[0]; r.Status != store.RunSuccess || *r.InstanceID != "main" || r.ErrorCode != nil {
		t.Fatalf("after retry %+v", r)
	}
}

func TestRecoverOnStart(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select 1", reports.Input{})
	sys := h.Store.System()
	_ = sys.Heartbeat(h.Ctx, &store.Instance{ID: "previous", Hostname: "h", Version: "dev"}, h.Clock.Now())
	run, _, _ := h.reports.Run(h.Ctx, h.Principal, rp.ID, reports.RunInput{})
	_, _ = sys.Claim(h.Ctx, run.ID, "previous", h.Clock.Now())
	r := runner.New(runner.Config{Tick: time.Second, SpoolDir: h.spool, InstanceID: "next", RecoverOnStart: true}, h.Store, h.Queries, h.Conns, runner.Options{Now: h.Clock.Now})
	if err := r.Maintain(h.Ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.runs(t, rp.ID)[0]; got.Status != store.RunPending || got.Attempt != 2 {
		t.Fatalf("run %+v", got)
	}
}

func TestRunAndGracefulShutdown(t *testing.T) {
	h := newHarness(t)
	quick := h.report(t, "select 1", reports.Input{})
	slow := h.report(t, "select 1 -- block", reports.Input{})
	r := runner.New(runner.Config{Workers: 2, Tick: 50 * time.Millisecond, SpoolDir: filepath.Join(h.spool, "run"), InstanceID: "svc", ShutdownTimeout: 200 * time.Millisecond},
		h.Store, h.Queries, h.Conns, runner.Options{Now: h.Clock.Now})
	svc := reports.NewService(h.Store, h.Queries, reports.Options{Now: h.Clock.Now, Wake: r.Wake, CancelLocal: r.Cancel})
	ctx, stop := context.WithCancel(h.Ctx)
	stopped := make(chan error, 1)
	go func() { stopped <- r.Run(ctx) }()

	_, _, _ = svc.Run(h.Ctx, h.Principal, quick.ID, reports.RunInput{})
	deadline := time.After(10 * time.Second)
	for h.runs(t, quick.ID)[0].Status != store.RunSuccess {
		select {
		case <-deadline:
			t.Fatal("the quick run did not finish")
		case <-time.After(20 * time.Millisecond):
		}
	}
	_, _, _ = svc.Run(h.Ctx, h.Principal, slow.ID, reports.RunInput{})
	<-fakeStarted
	stop()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the runner did not stop")
	}
	if got := h.runs(t, slow.ID)[0]; got.Status != store.RunCancelled || code(got) != runner.CodeShutdown {
		t.Fatalf("slow run %+v", got)
	}
	var alive int
	for _, id := range []string{"svc"} {
		if orphans, _ := h.Store.System().OrphanedRuns(h.Ctx, h.Clock.Now(), id); len(orphans) > 0 {
			alive++
		}
	}
	if alive != 0 {
		t.Error("runs left running after shutdown")
	}
}

func TestSpoolRetention(t *testing.T) {
	h := newHarness(t)
	ok := h.report(t, "select 1", reports.Input{})
	bad := h.report(t, "select 1 -- auth", reports.Input{})
	for _, id := range []uuid.UUID{ok.ID, bad.ID} {
		_, _, _ = h.reports.Run(h.Ctx, h.Principal, id, reports.RunInput{})
		h.process(t, h.run)
	}
	good, failed := h.runs(t, ok.ID)[0], h.runs(t, bad.ID)[0]
	if good.ResultExpiresAt == nil || failed.ResultExpiresAt != nil {
		t.Fatalf("expiry: %v %v", good.ResultExpiresAt, failed.ResultExpiresAt)
	}
	if _, err := os.Stat(runner.SpoolPath(h.spool, failed.ID)); !os.IsNotExist(err) {
		t.Errorf("a failed run kept its spool: %v", err)
	}
	// A day later the maintenance removes the spool.
	path := runner.SpoolPath(h.spool, good.ID)
	old := time.Now().Add(-25 * time.Hour)
	_ = os.Chtimes(path, old, old)
	h.Clock.Set(time.Now())
	if err := h.run.Maintain(h.Ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expired spool still there: %v", err)
	}
}

func TestDownloadResult(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select id, region, total from orders order by id", reports.Input{Title: "Pedidos"})
	bad := h.report(t, "select 1 -- auth", reports.Input{})
	for _, id := range []uuid.UUID{rp.ID, bad.ID} {
		_, _, _ = h.reports.Run(h.Ctx, h.Principal, id, reports.RunInput{})
		h.process(t, h.run)
	}
	run := h.runs(t, rp.ID)[0]

	f, err := h.reports.RunResult(h.Ctx, h.Principal, run.ID, "csv")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(f.Body)
	_ = f.Body.Close()
	if f.Name != rp.Slug+"-2026-09-25-1200.csv" {
		t.Errorf("file name %q", f.Name)
	}
	if !strings.HasPrefix(string(body), "id,region,total\r\n1,south,120.5") || f.Size != int64(len(body)) || f.ContentType != "text/csv; charset=utf-8" {
		t.Errorf("csv %q size %d type %s", body, f.Size, f.ContentType)
	}
	x, err := h.reports.RunResult(h.Ctx, h.Principal, run.ID, "xlsx")
	if err != nil || x.Size == 0 {
		t.Fatalf("xlsx %v", err)
	}
	_ = x.Body.Close()
	if entries, _ := os.ReadDir(h.spool); len(entries) != 1 {
		t.Errorf("temporary files left: %v", entries)
	}

	check := func(err error, code string) {
		t.Helper()
		ae, ok := apperr.As(err)
		if !ok || (ae.Code != code && (len(ae.Fields) == 0 || ae.Fields[0].Code != code)) {
			t.Errorf("error %v, want %s", err, code)
		}
	}
	_, err = h.reports.RunResult(h.Ctx, h.Principal, run.ID, "html_table")
	check(err, "validation.invalid_value")
	_, err = h.reports.RunResult(h.Ctx, h.Principal, run.ID, "")
	check(err, "validation.required")
	_, err = h.reports.RunResult(h.Ctx, h.Principal, h.runs(t, bad.ID)[0].ID, "csv")
	check(err, "run.result_unavailable")
	h.Clock.Advance(25 * time.Hour)
	_, err = h.reports.RunResult(h.Ctx, h.Principal, run.ID, "csv")
	check(err, "run.result_unavailable")
}
