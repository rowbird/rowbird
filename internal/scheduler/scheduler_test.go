package scheduler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/scheduler"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

// TestConcurrentSchedulers runs several schedulers at once, as several instances would on
// Postgres: every due report gets exactly one run for its slot.
func TestConcurrentSchedulers(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		w := &store.Workspace{Name: "w", Slug: "w"}
		if err := s.Workspaces().Create(t.Context(), w); err != nil {
			t.Fatal(err)
		}
		ctx := store.WithWorkspace(context.Background(), w.ID)
		conn := &store.Connection{Name: "db", Driver: "sqlite", Config: map[string]any{}}
		if err := s.Connections().Create(ctx, conn); err != nil {
			t.Fatal(err)
		}
		q := &store.Query{Title: "q", Slug: "q", ConnectionID: conn.ID}
		if err := s.Queries().Create(ctx, q); err != nil {
			t.Fatal(err)
		}
		slot := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
		const reports = 120
		for i := range reports {
			rp := &store.Report{
				Title: "r", Slug: fmt.Sprintf("r%d", i), QueryID: q.ID, Enabled: true, Cron: "0 * * * *", Timezone: "UTC",
				NextRunAt: &slot, Condition: json.RawMessage(`{"match":"all","rules":[]}`),
				RetryMax: 2, RetryBackoffSeconds: 30, MisfirePolicy: store.MisfireRunOnce, OverlapPolicy: store.OverlapSkip, AutoPauseAfter: 5,
			}
			if err := s.Reports().Create(ctx, rp); err != nil {
				t.Fatal(err)
			}
		}
		now := func() time.Time { return slot.Add(10 * time.Second) }
		var wg sync.WaitGroup
		var mu sync.Mutex
		total := 0
		for range 4 {
			wg.Go(func() {
				n, err := scheduler.New(s, scheduler.Options{Now: now}).Tick(context.Background())
				if err != nil {
					t.Error(err)
				}
				mu.Lock()
				total += n
				mu.Unlock()
			})
		}
		wg.Wait()
		page, err := s.Runs().List(ctx, store.RunFilter{}, store.PageRequest{Limit: store.MaxPageLimit})
		if err != nil {
			t.Fatal(err)
		}
		if total != reports || len(page.Items) != reports {
			t.Fatalf("queued %d, stored %d, want %d", total, len(page.Items), reports)
		}
		list, _ := s.Reports().List(ctx)
		for _, rp := range list {
			if !rp.NextRunAt.Equal(slot.Add(time.Hour)) {
				t.Fatalf("report %s next %v", rp.Slug, rp.NextRunAt)
			}
		}
	})
}

func TestHealthy(t *testing.T) {
	s := storetest.Open(t, "sqlite://"+t.TempDir()+"/s.db")
	if _, err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	sch := scheduler.New(s, scheduler.Options{Now: clock, Tick: time.Hour})
	if sch.Healthy() {
		t.Fatal("healthy before any tick")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { sch.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for !sch.Healthy() {
		if time.Now().After(deadline) {
			t.Fatal("not healthy after the first tick")
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	now = now.Add(4 * time.Hour)
	mu.Unlock()
	if sch.Healthy() {
		t.Fatal("healthy although no tick completed for three ticks")
	}
	cancel()
	<-done
}
