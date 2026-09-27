package gitops_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/store"
)

func TestApplyDir(t *testing.T) {
	src := newWorkspace(t)
	src.seed(t)
	text := src.export(t)
	w := newWorkspace(t)
	dir := t.TempDir()
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "rowbird.yaml"), []byte(strings.ReplaceAll(s, "/data/sqlite", w.Dir)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := func(name string) (string, bool) { return "hook-secret-value", name == hookSecretEnv }
	apply := func() *struct{ orphans, detached int } {
		t.Helper()
		res, err := w.planner().ApplyDir(w.Ctx, w.Principal, dir, env, auth.RequestMeta{})
		if err != nil {
			fatal(t, err)
		}
		return &struct{ orphans, detached int }{len(res.Orphaned), len(res.Detached)}
	}

	write(text)
	apply()
	rp, _ := w.Store.Reports().GetBySlug(w.Ctx, "daily-orders")
	q, _ := w.Store.Queries().GetBySlug(w.Ctx, "orders-by-region")
	ch, _ := w.Store.Channels().GetByName(w.Ctx, "erp-hook")
	shop, _ := w.Store.Connections().GetByName(w.Ctx, "shop")
	for name, by := range map[string]string{"report": rp.ManagedBy, "query": q.ManagedBy, "channel": ch.ManagedBy, "connection": shop.ManagedBy} {
		if by != store.ManagedByGitOps {
			t.Errorf("%s managed by %q", name, by)
		}
	}
	// The UI and the API cannot change them.
	title := "edited"
	if _, err := w.Queries.Update(w.Ctx, w.Principal, q.ID, queries.Patch{Version: q.Version, Title: &title}); !errors.Is(err, store.ErrManaged) {
		t.Fatalf("edit of a GitOps query: %v", err)
	}
	if err := w.reports.Delete(w.Ctx, rp.ID); !errors.Is(err, store.ErrManaged) {
		t.Fatalf("delete of a GitOps report: %v", err)
	}

	// A report that leaves the directory is paused as an orphan, once.
	write(text[:strings.Index(text, "---\napiVersion: rowbird.dev/v1\nkind: Report")])
	if got := apply(); got.orphans != 1 {
		t.Fatalf("orphans %d", got.orphans)
	}
	rp, _ = w.Store.Reports().GetBySlug(w.Ctx, "daily-orders")
	if rp.Enabled || !rp.GitOpsOrphan || rp.PausedReason == nil || *rp.PausedReason != store.PausedGitOpsOrphan {
		t.Fatalf("orphan %+v", rp)
	}
	if got := apply(); got.orphans != 0 {
		t.Errorf("orphaned again: %d", got.orphans)
	}
	// Back in the directory, it runs again.
	write(text)
	apply()
	rp, _ = w.Store.Reports().GetBySlug(w.Ctx, "daily-orders")
	if !rp.Enabled || rp.GitOpsOrphan {
		t.Fatalf("returned %+v", rp)
	}

	// A detached resource keeps the UI's changes.
	if err := w.Store.SetManagedBy(w.Ctx, "query", q.ID, store.ManagedByDetached); err != nil {
		t.Fatal(err)
	}
	write(strings.Replace(text, "group by region", "group by region order by 2 desc", 1))
	if got := apply(); got.detached != 1 {
		t.Fatalf("detached %d", got.detached)
	}
	v, _ := w.Queries.Get(w.Ctx, q.ID)
	if strings.Contains(v.Current.SQL, "order by") || v.Query.ManagedBy != store.ManagedByDetached {
		t.Errorf("a detached query was overwritten: %s %s", v.Current.SQL, v.Query.ManagedBy)
	}
}

func TestApplyDirReportsProblems(t *testing.T) {
	w := newWorkspace(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("apiVersion: rowbird.dev/v1\nkind: Query\nmetadata: {name: q}\nspec: {connection: nowhere, sql: select 1}\n"), 0o600)
	res, err := w.planner().ApplyDir(w.Ctx, w.Principal, dir, nil, auth.RequestMeta{})
	if !errors.Is(err, errBlocked()) || len(res.Plan.Missing) != 1 {
		t.Fatalf("missing connection: %v %+v", err, res)
	}
}
