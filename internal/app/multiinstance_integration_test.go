//go:build integration

package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/config"
	"github.com/rowbird/rowbird/internal/gitops"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
	"github.com/rowbird/rowbird/internal/testenv"
)

// TestInstancesShareAPostgresStore runs two instances on one Postgres store, as a multi-instance
// deployment does (docs/spec/08-operations.md, "Scaling & HA"): every due report runs exactly once
// between them, a run left behind by an instance that died is failed and retried, and the daily
// retention job runs on one instance only.
func TestInstancesShareAPostgresStore(t *testing.T) {
	url := storetest.Dialects()[store.Postgres]
	if url == nil {
		t.Skip("no Postgres")
	}
	dbURL := url(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sqliteDir := t.TempDir()
	shop := filepath.Join(sqliteDir, "shop.db")
	db, err := sql.Open("sqlite", "file:"+shop)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), testenv.ShopSQL); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	dataDir := t.TempDir() // shared, like a volume both instances mount

	start := func(name string) (*App, context.CancelFunc, chan error) {
		cfg, err := config.Load(config.Options{Environ: func() []string {
			return []string{
				"ROWBIRD_DATABASE_URL=" + dbURL, "ROWBIRD_DATA_DIR=" + dataDir, "ROWBIRD_SQLITE_DIRS=" + sqliteDir,
				"ROWBIRD_MASTER_KEY=" + base64.StdEncoding.EncodeToString(key), "ROWBIRD_SCHEDULER_TICK=1s",
				"ROWBIRD_WORKERS=3", "ROWBIRD_UPDATE_CHECK=false", "ROWBIRD_BASE_URL=http://localhost",
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})).With("instance", name)
		ctx, cancel := context.WithCancel(context.Background())
		a, err := New(ctx, cfg, logger, Options{})
		if err != nil {
			t.Fatal(err)
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- a.Serve(ctx, ln) }()
		return a, cancel, done
	}

	a1, stop1, done1 := start("one")
	a2, stop2, done2 := start("two")
	defer func() {
		stop1()
		stop2()
		<-done1
		<-done2
	}()

	// Setup, then ten reports from YAML.
	svc := auth.NewService(a1.store, a1.keyring, auth.Config{}, auth.Options{})
	if _, err := svc.Setup(t.Context(), auth.SetupInput{Email: "admin@example.com", Name: "Admin", Password: "admin passphrase 1", Locale: "en", Timezone: "UTC"}, auth.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	var yaml strings.Builder
	fmt.Fprintf(&yaml, "apiVersion: rowbird.dev/v1\nkind: Connection\nmetadata: {name: shop}\nspec: {driver: sqlite, config: {path: %q}}\n", shop)
	const reports = 12
	for i := range reports {
		fmt.Fprintf(&yaml, "---\napiVersion: rowbird.dev/v1\nkind: Report\nmetadata: {name: r%d}\nspec:\n  query: {connection: shop, sql: \"WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 60000) SELECT COUNT(*) AS n FROM c\"}\n  schedule: {cron: \"0 3 * * *\", timezone: UTC}\n  run: {retryMax: 1, retryBackoffSeconds: 1}\n", i)
	}
	docs, err := gitops.Parse("reports.yaml", []byte(yaml.String()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, p, err := a1.AdminContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := a1.Planner().Plan(ctx, docs, gitops.Options{Policy: gitops.PolicyOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	if err := a1.Planner().Apply(ctx, p, plan, auth.RequestMeta{}, false); err != nil {
		t.Fatal(err)
	}

	// Every report falls due at once; both schedulers race for them.
	sc, err := a1.store.Scoped(ctx)
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(-time.Second).UTC()
	if _, err := sc.NewUpdate((*store.Report)(nil)).Set("next_run_at = ?", due).Where("rp.enabled = ?", true).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var runs []store.Run
	// Generous: CI machines are small and the race detector slows every run several times over.
	deadline := time.Now().Add(3 * time.Minute)
	for {
		page, err := a1.store.Runs().List(ctx, store.RunFilter{}, store.PageRequest{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		runs = page.Items
		finished := 0
		for _, r := range runs {
			if r.Status == store.RunSuccess {
				finished++
			}
		}
		if finished >= reports {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d runs finished: %+v", finished, reports, runs)
		}
		time.Sleep(200 * time.Millisecond)
	}
	perReport := map[string]int{}
	perInstance := map[string]int{}
	for _, r := range runs {
		perReport[r.ReportID.String()]++
		if r.InstanceID != nil {
			perInstance[*r.InstanceID]++
		}
	}
	if len(runs) != reports || len(perReport) != reports {
		t.Fatalf("%d runs for %d reports: every due report must run exactly once", len(runs), len(perReport))
	}
	// Each instance has three workers and a query takes a moment, so both take a share.
	if len(perInstance) != 2 {
		t.Fatalf("runs per instance: %v; both instances should have worked", perInstance)
	}

	// A run left "running" by an instance that no longer exists is queued again as its next attempt
	// (retryMax is 1) and finishes on a live instance.
	rp, err := a1.store.Reports().GetBySlug(ctx, "r0")
	if err != nil {
		t.Fatal(err)
	}
	lost := &store.Run{ReportID: rp.ID, Trigger: store.TriggerManual, AvailableAt: time.Now().UTC()}
	if err := a1.store.Runs().Create(ctx, lost); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.NewUpdate((*store.Run)(nil)).Set("status = ?", store.RunRunning).Set("instance_id = ?", "ghost").
		Set("started_at = ?", time.Now().Add(-time.Hour).UTC()).Where("ru.id = ?", lost.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(60 * time.Second)
	for {
		got, err := a1.store.Runs().Get(ctx, lost.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == store.RunSuccess {
			if got.Attempt != 2 || got.InstanceID == nil || *got.InstanceID == "ghost" {
				t.Fatalf("lost run: %+v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lost run still %s", got.Status)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// The retention job runs once a day across instances.
	var wg sync.WaitGroup
	ran := make([]bool, 2)
	for i, a := range []*App{a1, a2} {
		wg.Go(func() {
			_, ok, err := a.retention.RunIfDue(context.Background())
			if err != nil {
				t.Error(err)
			}
			ran[i] = ok
		})
	}
	wg.Wait()
	// The instances' own loops may have taken it already at start; then neither runs it now.
	if ran[0] && ran[1] {
		t.Fatal("the retention job ran on both instances")
	}
	if last, err := a1.store.Maintenance().JobLastRun(t.Context(), "retention"); err != nil || last == nil {
		t.Fatalf("retention never ran: %v", err)
	}
}
