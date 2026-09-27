package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/store"
)

func TestConfigAsCodeAPI(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.setup()
	var conn gen.Connection
	admin.do(http.MethodPost, "/api/v1/connections", map[string]any{"name": "shop", "driver": "sqlite", "config": map[string]any{"path": ts.sqliteFixture("shop.db")}}).decode(t, &conn)

	yaml := `apiVersion: rowbird.dev/v1
kind: Query
metadata: { name: big-orders, title: Big orders }
spec:
  connection: warehouse
  sql: select 1
`
	var res gen.ImportResult
	admin.do(http.MethodPost, "/api/v1/import", map[string]any{"yaml": yaml}).decode(t, &res)
	if len(res.Missing) != 1 || res.Missing[0].Name != "warehouse" || res.Applied {
		t.Fatalf("missing %+v", res)
	}
	admin.do(http.MethodPost, "/api/v1/import", map[string]any{"yaml": yaml, "map": map[string]string{"warehouse": "shop"}}).decode(t, &res)
	if len(res.Items) != 1 || res.Items[0].Action != "create" || res.Applied || len(res.Errors) != 0 {
		t.Fatalf("dry run %+v", res)
	}
	admin.do(http.MethodPost, "/api/v1/import", map[string]any{"yaml": yaml, "map": map[string]string{"warehouse": "shop"}, "dry_run": false}).decode(t, &res)
	if !res.Applied {
		t.Fatalf("apply %+v", res)
	}

	res = gen.ImportResult{}
	admin.do(http.MethodPost, "/api/v1/import", map[string]any{"yaml": "apiVersion: rowbird.dev/v1\nkind: Query\nmetadata: {name: q}\nspec: {sqll: x}\n"}).decode(t, &res)
	if len(res.Errors) == 0 || res.Errors[0].Pos.Line != 4 || !strings.Contains(res.Errors[0].Message, `unknown field "sqll"`) {
		t.Fatalf("errors %+v", res.Errors)
	}

	r := admin.do(http.MethodPost, "/api/v1/export", map[string]any{"queries": []string{"big-orders"}, "include_connections": true})
	body := r.Body.String()
	if r.Code != http.StatusOK || !strings.HasPrefix(r.Header().Get("Content-Type"), "application/yaml") || !strings.Contains(body, "kind: Connection") || !strings.Contains(body, "name: big-orders") {
		t.Fatalf("export %d %s %s", r.Code, r.Header(), body)
	}
	if r := admin.do(http.MethodPost, "/api/v1/export", map[string]any{"reports": []string{"nope"}}); r.Code != http.StatusBadRequest {
		t.Fatalf("unknown report %d", r.Code)
	}

	// A GitOps connection is read only until an admin detaches it.
	ws, _ := ts.store.Workspaces().GetBySlug(t.Context(), auth.DefaultWorkspaceSlug)
	ctx := store.WithWorkspace(t.Context(), ws.ID)
	if err := ts.store.SetManagedBy(ctx, "connection", conn.Id, store.ManagedByGitOps); err != nil {
		t.Fatal(err)
	}
	var c gen.Connection
	admin.do(http.MethodGet, "/api/v1/connections/"+conn.Id.String(), nil).decode(t, &c)
	if c.ManagedBy != gen.Gitops {
		t.Fatalf("managed_by %q", c.ManagedBy)
	}
	r = admin.do(http.MethodPatch, "/api/v1/connections/"+conn.Id.String(), map[string]any{"version": c.Version, "max_rows": 10})
	if r.Code != http.StatusConflict || r.problem(t).Code != "resource.managed_by_gitops" {
		t.Fatalf("edit %d %s", r.Code, r.Body)
	}
	if r := admin.do(http.MethodPost, "/api/v1/gitops/attach", map[string]any{"kind": "connection", "id": conn.Id}); r.Code != http.StatusConflict {
		t.Fatalf("attach managed %d", r.Code)
	}
	if r := admin.do(http.MethodPost, "/api/v1/gitops/detach", map[string]any{"kind": "connection", "id": conn.Id}); r.Code != http.StatusNoContent {
		t.Fatalf("detach %d %s", r.Code, r.Body)
	}
	if r := admin.do(http.MethodPatch, "/api/v1/connections/"+conn.Id.String(), map[string]any{"version": c.Version, "max_rows": 10}); r.Code != http.StatusOK {
		t.Fatalf("edit after detach %d %s", r.Code, r.Body)
	}
}
