package api

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/api/gen"
)

func (ts *testServer) sqliteFixture(name string) string {
	ts.t.Helper()
	p := filepath.Join(ts.sqliteDir, name)
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		ts.t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE orders (id INTEGER, total DECIMAL(10,2)); CREATE VIEW big AS SELECT * FROM orders WHERE total > 100"); err != nil {
		ts.t.Fatal(err)
	}
	return p
}

func TestPluginCatalog(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.setup()
	var cat gen.PluginCatalog
	admin.do(http.MethodGet, "/api/v1/plugins", nil).decode(t, &cat)
	var sqlite *gen.PluginInfo
	for i := range cat.Plugins {
		if cat.Plugins[i].Id == "sqlite" {
			sqlite = &cat.Plugins[i]
		}
	}
	if sqlite == nil || sqlite.Kind != gen.PluginInfoKindConnector || sqlite.Schema["x-order"] == nil || sqlite.Capabilities["dialect"] != "sqlite" {
		t.Fatalf("sqlite plugin %+v", sqlite)
	}
	if cat.Messages["en"]["plugin.sqlite.name"] != "SQLite" || cat.Messages["pt-BR"]["plugin.sqlite.path.label"] == "" {
		t.Fatal("plugin messages missing")
	}
}

func TestConnectionsOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.setup()
	path := ts.sqliteFixture("shop.db")

	res := admin.do(http.MethodPost, "/api/v1/connections/test", map[string]any{"driver": "sqlite", "config": map[string]any{"path": path}})
	var tr gen.ConnectionTestResult
	res.decode(t, &tr)
	if !tr.Ok || tr.Tables == nil || *tr.Tables != 2 || tr.CanWrite == nil || *tr.CanWrite {
		t.Fatalf("test: %d %s", res.Code, res.Body)
	}
	res = admin.do(http.MethodPost, "/api/v1/connections/test", map[string]any{"driver": "sqlite", "config": map[string]any{"path": "/etc/hosts"}})
	res.decode(t, &tr)
	if tr.Ok || tr.ErrorCode == nil || *tr.ErrorCode != "connection.path_not_allowed" {
		t.Fatalf("failing test: %s", res.Body)
	}

	res = admin.do(http.MethodPost, "/api/v1/connections", map[string]any{"name": "Bad Name", "driver": "sqlite", "config": map[string]any{}})
	if p := res.problem(t); res.Code != 400 || p.Errors == nil || len(*p.Errors) != 2 {
		t.Fatalf("validation: %d %s", res.Code, res.Body)
	}
	res = admin.do(http.MethodPost, "/api/v1/connections", map[string]any{"name": "shop", "driver": "sqlite", "config": map[string]any{"path": path}, "ai_excluded_tables": []string{"big"}})
	var c gen.Connection
	res.decode(t, &c)
	if res.Code != http.StatusCreated || c.Status != gen.ConnectionStatusOk || c.SchemaCachedAt == nil || c.AiExcludedTables[0] != "big" {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	if p := admin.do(http.MethodPost, "/api/v1/connections", map[string]any{"name": "shop", "driver": "sqlite", "config": map[string]any{"path": path}}).problem(t); p.Code != "connection.name_taken" {
		t.Fatalf("duplicate: %+v", p)
	}

	var schema gen.DatabaseSchema
	admin.do(http.MethodGet, "/api/v1/connections/"+c.Id.String()+"/schema", nil).decode(t, &schema)
	if len(schema.Tables) != 2 || schema.Tables[0].Name != "big" || schema.Tables[0].Kind != gen.View || schema.Tables[1].Columns[1].Type != gen.DatabaseColumnTypeDecimal {
		t.Fatalf("schema %+v", schema)
	}

	res = admin.do(http.MethodPatch, "/api/v1/connections/"+c.Id.String(), map[string]any{"version": c.Version, "max_rows": 500})
	res.decode(t, &c)
	if res.Code != 200 || c.MaxRows != 500 {
		t.Fatalf("patch: %d %s", res.Code, res.Body)
	}
	var list gen.ConnectionList
	admin.do(http.MethodGet, "/api/v1/connections", nil).decode(t, &list)
	if len(list.Items) != 1 {
		t.Fatalf("list %+v", list)
	}
	if res := admin.do(http.MethodPost, "/api/v1/connections/"+c.Id.String()+"/test", nil); res.Code != 200 || !strings.Contains(res.Body.String(), `"ok":true`) {
		t.Fatalf("saved test: %s", res.Body)
	}
	if res := admin.do(http.MethodDelete, "/api/v1/connections/"+c.Id.String(), nil); res.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", res.Code)
	}
	if res := admin.do(http.MethodGet, "/api/v1/connections/"+c.Id.String(), nil); res.Code != 404 {
		t.Fatalf("after delete: %d", res.Code)
	}
}
