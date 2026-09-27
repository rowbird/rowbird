package api

import (
	"net/http"
	"testing"

	"github.com/rowbird/rowbird/internal/api/gen"
)

func TestQueriesOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.setup()
	path := ts.sqliteFixture("shop.db")
	var conn gen.Connection
	admin.do(http.MethodPost, "/api/v1/connections", map[string]any{"name": "shop", "driver": "sqlite", "config": map[string]any{"path": path}}).decode(t, &conn)

	res := admin.do(http.MethodPost, "/api/v1/queries/preview", map[string]any{
		"connection_id": conn.Id, "sql": "select {{n}} as n, {{today}} as d, 9007199254740993 as big, 1.5 as f",
		"params": []map[string]any{{"name": "n", "type": "integer", "default": "7"}}, "timezone": "America/Sao_Paulo",
	})
	var prev gen.PreviewResult
	res.decode(t, &prev)
	if res.Code != 200 || len(prev.Rows) != 1 || prev.Timezone != "America/Sao_Paulo" || len(prev.Params) != 2 {
		t.Fatalf("preview: %d %s", res.Code, res.Body)
	}
	row := prev.Rows[0]
	if row[0] != 7.0 || row[2] != "9007199254740993" || row[3] != 1.5 {
		t.Fatalf("row encoding %#v", row)
	}
	if p := admin.do(http.MethodPost, "/api/v1/queries/preview", map[string]any{"connection_id": conn.Id, "sql": "select * from nope"}).problem(t); p.Code != "query.failed" || p.Status != 422 {
		t.Fatalf("bad sql: %+v", p)
	}

	res = admin.do(http.MethodPost, "/api/v1/queries", map[string]any{"title": "Pedidos grandes", "connection_id": conn.Id, "sql": "select * from orders", "note": "first"})
	var q gen.Query
	res.decode(t, &q)
	if res.Code != 201 || q.Slug != "pedidos-grandes" || q.CurrentVersion != 1 || q.AuthorName != "Admin" || q.ConnectionName != "shop" {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	res = admin.do(http.MethodPatch, "/api/v1/queries/"+q.Id.String(), map[string]any{"version": q.Version, "sql": "select * from orders where total > 100", "note": "big ones"})
	res.decode(t, &q)
	if q.CurrentVersion != 2 || q.Current.Note != "big ones" {
		t.Fatalf("update: %s", res.Body)
	}
	var versions gen.QueryVersionList
	admin.do(http.MethodGet, "/api/v1/queries/"+q.Id.String()+"/versions", nil).decode(t, &versions)
	if len(versions.Items) != 2 || versions.Items[0].Number != 2 {
		t.Fatalf("versions %+v", versions)
	}
	var v1 gen.QueryVersion
	admin.do(http.MethodGet, "/api/v1/queries/"+q.Id.String()+"/versions/1", nil).decode(t, &v1)
	if v1.Sql != "select * from orders" {
		t.Fatalf("v1 %+v", v1)
	}
	res = admin.do(http.MethodPost, "/api/v1/queries/"+q.Id.String()+"/restore", map[string]any{"number": 1, "version": q.Version})
	res.decode(t, &q)
	if q.CurrentVersion != 3 || q.Current.RestoredFrom == nil || *q.Current.RestoredFrom != 1 || q.Sql != "select * from orders" {
		t.Fatalf("restore: %s", res.Body)
	}

	// The connection now has a dependent.
	res = admin.do(http.MethodDelete, "/api/v1/connections/"+conn.Id.String(), nil)
	if p := res.problem(t); res.Code != 409 || p.Code != "connection.in_use" || p.Dependents == nil || (*p.Dependents)[0].Name != "Pedidos grandes" {
		t.Fatalf("in use: %d %s", res.Code, res.Body)
	}
	if res := admin.do(http.MethodDelete, "/api/v1/queries/"+q.Id.String(), nil); res.Code != 204 {
		t.Fatalf("delete query: %d", res.Code)
	}
}
