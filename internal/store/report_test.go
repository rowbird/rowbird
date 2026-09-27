package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

// newReport creates a connection, a query and a report in the workspace bound to ctx.
func newReport(t *testing.T, s *store.Store, ctx context.Context, slug string, next *time.Time) *store.Report {
	t.Helper()
	conn := &store.Connection{Name: "db-" + slug, Driver: "sqlite", Config: map[string]any{}}
	if err := s.Connections().Create(ctx, conn); err != nil {
		t.Fatal(err)
	}
	q := &store.Query{Title: slug, Slug: slug, ConnectionID: conn.ID}
	if err := s.Queries().Create(ctx, q); err != nil {
		t.Fatal(err)
	}
	rp := &store.Report{
		Title: slug, Slug: slug, QueryID: q.ID, Enabled: next != nil, Cron: "* * * * *", Timezone: "UTC",
		NextRunAt: next, Condition: json.RawMessage(`{"match":"all","rules":[]}`),
		RetryMax: 2, RetryBackoffSeconds: 30, MisfirePolicy: store.MisfireRunOnce, OverlapPolicy: store.OverlapSkip, AutoPauseAfter: 5,
	}
	if err := s.Reports().Create(ctx, rp); err != nil {
		t.Fatal(err)
	}
	return rp
}

func ts(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t.UTC()
}

func ptr[T any](v T) *T { return &v }

func TestReports(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctxA := newWorkspace(t, s, "a")
		ctxB := newWorkspace(t, s, "b")
		rp := newReport(t, s, ctxA, "daily", ptr(ts("2026-09-25T10:00:00Z")))
		repo := s.Reports()

		dup := *rp
		dup.ID = uuid.Nil
		if err := repo.Create(ctxA, &dup); !errors.Is(err, store.ErrDuplicate) {
			t.Fatalf("duplicate slug: %v", err)
		}
		got, err := repo.Get(ctxA, rp.ID)
		if err != nil || got.Cron != "* * * * *" || !got.Enabled || !got.NextRunAt.Equal(ts("2026-09-25T10:00:00Z")) || got.ParamOverrides == nil {
			t.Fatalf("get: %+v %v", got, err)
		}
		if _, err := repo.Get(ctxB, rp.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("visible from B: %v", err)
		}

		// System changes do not bump the version.
		if err := repo.SetNextRun(ctxA, rp.ID, ptr(ts("2026-09-25T10:01:00Z"))); err != nil {
			t.Fatal(err)
		}
		if err := repo.RecordOutcome(ctxA, rp.ID, 5, ptr("hash"), true); err != nil {
			t.Fatal(err)
		}
		got, _ = repo.Get(ctxA, rp.ID)
		if got.Version != 1 || got.Enabled || got.NextRunAt != nil || *got.PausedReason != store.PausedAutoFailures ||
			got.ConsecutiveFailures != 5 || *got.LastResultHash != "hash" {
			t.Fatalf("after outcome: %+v", got)
		}
		if err := repo.SetNextRun(ctxB, rp.ID, nil); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("B moved A's schedule: %v", err)
		}

		got.Title = "Daily sales"
		if err := repo.Update(ctxA, got, "title"); err != nil || got.Version != 2 {
			t.Fatalf("update: %v", err)
		}
		counts, _ := repo.CountByQuery(ctxA)
		if counts[rp.QueryID] != 1 {
			t.Fatalf("counts %v", counts)
		}
		by, _ := repo.ListByQuery(ctxA, rp.QueryID)
		if len(by) != 1 {
			t.Fatalf("by query %d", len(by))
		}

		if err := s.Runs().Create(ctxA, &store.Run{ReportID: rp.ID, Trigger: store.TriggerManual, AvailableAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := repo.Delete(ctxA, rp.ID); err != nil {
			t.Fatal(err)
		}
		if page, _ := s.Runs().List(ctxA, store.RunFilter{}, store.PageRequest{}); len(page.Items) != 0 {
			t.Fatalf("runs survived the report: %d", len(page.Items))
		}
	})
}

func TestRuns(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctxA := newWorkspace(t, s, "a")
		ctxB := newWorkspace(t, s, "b")
		rp := newReport(t, s, ctxA, "r", nil)
		runs := s.Runs()
		at := ts("2026-09-25T10:00:00Z")

		slot := &store.Run{ReportID: rp.ID, Trigger: store.TriggerSchedule, ScheduledFor: &at, AvailableAt: at}
		if err := runs.Create(ctxA, slot); err != nil || slot.Status != store.RunPending || slot.Attempt != 1 {
			t.Fatalf("create: %+v %v", slot, err)
		}
		if err := runs.Create(ctxA, &store.Run{ReportID: rp.ID, Trigger: store.TriggerSchedule, ScheduledFor: &at, AvailableAt: at}); !errors.Is(err, store.ErrDuplicate) {
			t.Fatalf("second run for the same slot: %v", err)
		}
		manual := &store.Run{ReportID: rp.ID, Trigger: store.TriggerManual, IdempotencyKey: ptr("k1"), AvailableAt: at}
		if err := runs.Create(ctxA, manual); err != nil {
			t.Fatal(err)
		}
		if got, err := runs.ByIdempotencyKey(ctxA, rp.ID, "k1", at.Add(-time.Hour)); err != nil || got.ID != manual.ID {
			t.Fatalf("idempotency: %v", err)
		}
		if _, err := runs.ByIdempotencyKey(ctxA, rp.ID, "k1", time.Now().Add(time.Hour)); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("expired key found: %v", err)
		}
		if n, _ := runs.CountActive(ctxA, rp.ID); n != 2 {
			t.Fatalf("active %d", n)
		}

		// Claim, release for a retry, claim again, finish.
		sys := s.System()
		pending, err := sys.LockPendingRuns(ctxA, at, 10)
		if err != nil || len(pending) != 2 || pending[0].ID != slot.ID || pending[0].WorkspaceID != slot.WorkspaceID || pending[0].Trigger != store.TriggerSchedule {
			t.Fatalf("pending %+v %v", pending, err)
		}
		if ok, err := sys.Claim(ctxA, slot.ID, "i1", at); !ok || err != nil {
			t.Fatalf("claim %v %v", ok, err)
		}
		if ok, _ := sys.Claim(ctxA, slot.ID, "i2", at); ok {
			t.Fatal("claimed twice")
		}
		if running, _ := sys.HasRunningRun(ctxA, rp.ID); !running {
			t.Fatal("no running run")
		}
		if ok, _ := runs.Release(ctxA, slot.ID, "i2", 2, at, "", ""); ok {
			t.Fatal("released by an instance that does not own it")
		}
		if ok, _ := runs.Release(ctxA, slot.ID, "i1", 2, at.Add(time.Minute), "query.timeout", "timeout"); !ok {
			t.Fatal("release")
		}
		if pending, _ := sys.LockPendingRuns(ctxA, at, 10); len(pending) != 1 {
			t.Fatalf("released run available too early: %d", len(pending))
		}
		_, _ = sys.Claim(ctxA, slot.ID, "i1", at.Add(time.Minute))
		fin := at.Add(2 * time.Minute)
		slot.Status, slot.FinishedAt, slot.RowCount, slot.ResultSample = store.RunSuccess, &fin, ptr[int64](3), json.RawMessage(`{"columns":[],"rows":[]}`)
		if ok, err := runs.Finish(ctxB, slot, "i1"); ok || err != nil {
			t.Fatalf("B finished A's run: %v %v", ok, err)
		}
		if ok, err := runs.Finish(ctxA, slot, "i1"); !ok || err != nil {
			t.Fatalf("finish %v %v", ok, err)
		}
		got, _ := runs.Get(ctxA, slot.ID)
		if got.Status != store.RunSuccess || got.Attempt != 2 || *got.RowCount != 3 || got.ErrorCode != nil || string(got.ResultSample) == "" {
			t.Fatalf("finished run %+v", got)
		}

		// Cancel paths.
		if ok, _ := runs.RequestCancel(ctxA, manual.ID, fin); ok {
			t.Fatal("cancel requested on a pending run")
		}
		if ok, _ := runs.CancelPending(ctxA, manual.ID, fin, "cancelled"); !ok {
			t.Fatal("cancel pending")
		}
		if ok, _ := runs.CancelPending(ctxA, manual.ID, fin, "cancelled"); ok {
			t.Fatal("cancelled twice")
		}

		page, _ := runs.List(ctxA, store.RunFilter{ReportID: rp.ID, Statuses: []string{store.RunSuccess}}, store.PageRequest{})
		if len(page.Items) != 1 || page.Items[0].ResultSample != nil {
			t.Fatalf("filtered list %+v", page.Items)
		}
		page, _ = runs.List(ctxA, store.RunFilter{Trigger: store.TriggerManual}, store.PageRequest{Limit: 1})
		if len(page.Items) != 1 || page.NextCursor != "" {
			t.Fatalf("trigger filter %+v", page)
		}
		latest, err := runs.Latest(ctxA)
		if err != nil {
			t.Fatal(err)
		}
		if latest[rp.ID].ID != manual.ID {
			t.Fatalf("latest %+v", latest)
		}
	})
}

func TestSchedulerQueries(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctxA := newWorkspace(t, s, "a")
		ctxB := newWorkspace(t, s, "b")
		due := newReport(t, s, ctxA, "due", ptr(ts("2026-09-25T10:00:00Z")))
		dueB := newReport(t, s, ctxB, "due-b", ptr(ts("2026-09-25T09:00:00Z")))
		newReport(t, s, ctxA, "later", ptr(ts("2026-09-25T11:00:00Z")))
		newReport(t, s, ctxA, "paused", nil)
		sys := s.System()

		refs, err := sys.LockDueReports(context.Background(), ts("2026-09-25T10:00:00Z"), 10)
		if err != nil || len(refs) != 2 || refs[0].ID != dueB.ID || refs[1].ID != due.ID || refs[1].WorkspaceID != due.WorkspaceID {
			t.Fatalf("due %+v %v", refs, err)
		}
		if err := s.RunInTx(context.Background(), func(ctx context.Context) error {
			if err := sys.LockReport(ctx, due.ID); err != nil {
				return err
			}
			return s.Reports().SetNextRun(refs[1].Context(ctx), due.ID, ptr(ts("2026-09-25T10:01:00Z")))
		}); err != nil {
			t.Fatal(err)
		}

		// Instances and orphaned runs.
		now := ts("2026-09-25T10:00:00Z")
		if err := sys.Heartbeat(context.Background(), &store.Instance{ID: "old", Hostname: "h", Version: "dev"}, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		live := &store.Instance{ID: "live", Hostname: "h", Version: "dev"}
		_ = sys.Heartbeat(context.Background(), live, now.Add(-time.Hour))
		_ = sys.Heartbeat(context.Background(), live, now)
		for _, inst := range []string{"old", "live"} {
			run := &store.Run{ReportID: due.ID, Trigger: store.TriggerManual, AvailableAt: now}
			_ = s.Runs().Create(ctxA, run)
			_, _ = sys.Claim(ctxA, run.ID, inst, now)
		}
		orphans, err := sys.OrphanedRuns(context.Background(), now.Add(-time.Minute), "")
		if err != nil || len(orphans) != 1 {
			t.Fatalf("orphans %+v %v", orphans, err)
		}
		if all, _ := sys.OrphanedRuns(context.Background(), now, "live"); len(all) != 1 {
			t.Fatalf("other instances' runs %+v", all)
		}
		if err := sys.RemoveInstance(context.Background(), "live", now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		if orphans, _ := sys.OrphanedRuns(context.Background(), now.Add(-time.Minute), ""); len(orphans) != 2 {
			t.Fatalf("orphans after removal %d", len(orphans))
		}
	})
}

// TestConcurrentClaims races workers over the same queue: every run is claimed exactly once.
func TestConcurrentClaims(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := newWorkspace(t, s, "a")
		rp := newReport(t, s, ctx, "r", nil)
		now := time.Now().UTC()
		const total = 30
		for range total {
			if err := s.Runs().Create(ctx, &store.Run{ReportID: rp.ID, Trigger: store.TriggerManual, AvailableAt: now}); err != nil {
				t.Fatal(err)
			}
		}
		var mu sync.Mutex
		claimed := map[uuid.UUID]int{}
		var wg sync.WaitGroup
		for w := range 6 {
			wg.Go(func() {
				for {
					var got uuid.UUID
					err := s.RunInTx(context.Background(), func(ctx context.Context) error {
						pending, err := s.System().LockPendingRuns(ctx, now, 1)
						if err != nil || len(pending) == 0 {
							return err
						}
						ok, err := s.System().Claim(ctx, pending[0].ID, string(rune('a'+w)), now)
						if ok {
							got = pending[0].ID
						}
						return err
					})
					if err != nil {
						t.Error(err)
						return
					}
					if got == uuid.Nil {
						return
					}
					mu.Lock()
					claimed[got]++
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		if len(claimed) != total {
			t.Fatalf("claimed %d runs, want %d", len(claimed), total)
		}
		for id, n := range claimed {
			if n != 1 {
				t.Errorf("run %s claimed %d times", id, n)
			}
		}
	})
}
