package queries_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/connections"
	_ "github.com/rowbird/rowbird/internal/connector/sqlite"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/params"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

type env struct {
	svc   *queries.Service
	conns *connections.Service
	ctx   context.Context
	p     *auth.Principal
	conn  *connections.View
}

func newEnv(t *testing.T, s *store.Store) *env {
	t.Helper()
	w := &store.Workspace{Name: "w", Slug: "w"}
	if err := s.Workspaces().Create(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	u := &store.User{Email: "a@example.com", Name: "A", Locale: "en", Theme: "system"}
	_ = s.Users().Create(t.Context(), u)
	ctx := store.WithWorkspace(t.Context(), w.ID)
	if err := s.Settings().Put(ctx, auth.SettingDefaultTimezone, `"America/Sao_Paulo"`, false); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	kr, _ := crypto.NewKeyring(key)
	dir := t.TempDir()
	path := filepath.Join(dir, "shop.db")
	db, _ := sql.Open("sqlite", "file:"+path)
	_, err := db.Exec(`CREATE TABLE orders (id INTEGER, region TEXT, total DECIMAL(10,2), created_at DATETIME);
		INSERT INTO orders VALUES (1, 'south', 120.5, '2026-09-24 10:00:00'), (2, 'north', 80, '2026-09-25 09:00:00'),
			(3, 'south', 10, '2026-09-20 09:00:00'), (4, 'o''reilly', 1, '2026-09-25 09:00:00')`)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	conns := connections.NewService(s, kr, connections.Options{Dial: netx.NewDialer(netx.PolicyOpen).DialContext, SQLiteDirs: []string{dir}})
	p := &auth.Principal{UserID: u.ID, WorkspaceID: w.ID, Role: store.RoleAdmin}
	conn, err := conns.Create(ctx, p, connections.Input{Name: "shop", Driver: "sqlite", Config: map[string]any{"path": path}}, auth.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	// 2026-09-25 12:00 UTC is 09:00 in São Paulo: "today" is the 25th, "yesterday" the 24th.
	now := func() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) }
	return &env{svc: queries.NewService(s, conns, queries.Options{Now: now}), conns: conns, ctx: ctx, p: p, conn: conn}
}

func fields(t *testing.T, err error) map[string]string {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Kind != apperr.KindInvalid {
		t.Fatalf("got %v, want a validation error", err)
	}
	out := map[string]string{}
	for _, f := range e.Fields {
		out[f.Field] = f.Code
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func TestLifecycle(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s)

		_, err := e.svc.Create(e.ctx, e.p, queries.Input{Title: "", Slug: "Bad Slug", ConnectionID: e.conn.ID, SQL: "select {{ghost}}"})
		f := fields(t, err)
		if f["title"] != "validation.required" || f["slug"] != "validation.slug" {
			t.Fatalf("fields %v", f)
		}
		_, err = e.svc.Create(e.ctx, e.p, queries.Input{Title: "X", ConnectionID: e.conn.ID, SQL: "select {{ghost}}"})
		if fields(t, err)["sql.ghost"] != params.CodeUndefined {
			t.Fatal("undefined parameter accepted")
		}

		v, err := e.svc.Create(e.ctx, e.p, queries.Input{Title: "Vendas por região", ConnectionID: e.conn.ID, SQL: "select * from orders", Note: "first"})
		if err != nil {
			t.Fatal(err)
		}
		if v.Query.Slug != "vendas-por-regiao" || v.Current.Number != 1 || v.ConnectionName != "shop" {
			t.Fatalf("created %+v %+v", v.Query, v.Current)
		}
		if _, err := e.svc.Create(e.ctx, e.p, queries.Input{Title: "Vendas por Região!", ConnectionID: e.conn.ID, SQL: "select 1"}); !errors.Is(err, queries.ErrSlugTaken) {
			t.Fatalf("slug collision: %v", err)
		}

		// Title only: no new version.
		v, err = e.svc.Update(e.ctx, e.p, v.Query.ID, queries.Patch{Version: v.Query.Version, Title: ptr("Sales by region")})
		if err != nil || v.Current.Number != 1 || v.Query.Title != "Sales by region" {
			t.Fatalf("title update: %+v %v", v, err)
		}
		v, err = e.svc.Update(e.ctx, e.p, v.Query.ID, queries.Patch{
			Version: v.Query.Version, SQL: ptr("select * from orders where region = {{region}}"),
			Params: &[]params.Definition{{Name: "region", Type: params.Text, Default: ptr("south")}}, Note: "filter",
		})
		if err != nil || v.Current.Number != 2 || v.Current.Note != "filter" {
			t.Fatalf("sql update: %+v %v", v.Current, err)
		}
		v, err = e.svc.Update(e.ctx, e.p, v.Query.ID, queries.Patch{Version: v.Query.Version, SQL: ptr(v.Current.SQL)})
		if err != nil || v.Current.Number != 2 {
			t.Fatal("identical SQL created a version")
		}
		if _, err := e.svc.Update(e.ctx, e.p, v.Query.ID, queries.Patch{Version: 1, Title: ptr("stale")}); !errors.Is(err, store.ErrConflict) {
			t.Fatalf("stale version: %v", err)
		}

		v, err = e.svc.Restore(e.ctx, e.p, v.Query.ID, 1, v.Query.Version)
		if err != nil || v.Current.Number != 3 || v.Current.SQL != "select * from orders" || v.Current.RestoredFrom == nil || *v.Current.RestoredFrom != 1 {
			t.Fatalf("restore: %+v %v", v.Current, err)
		}
		versions, _ := e.svc.Versions(e.ctx, v.Query.ID)
		if len(versions) != 3 || versions[0].Number != 3 {
			t.Fatalf("versions %d", len(versions))
		}

		// The connection cannot go while the query uses it.
		err = e.conns.Delete(e.ctx, e.p, e.conn.ID, auth.RequestMeta{})
		ae, ok := apperr.As(err)
		if !ok || ae.Code != "connection.in_use" || len(ae.Dependents) != 1 || ae.Dependents[0].Name != "Sales by region" {
			t.Fatalf("connection delete: %v", err)
		}
		if err := e.svc.Delete(e.ctx, v.Query.ID); err != nil {
			t.Fatal(err)
		}
		if err := e.conns.Delete(e.ctx, e.p, e.conn.ID, auth.RequestMeta{}); err != nil {
			t.Fatalf("connection delete after the query went: %v", err)
		}
	})
}

func TestPreview(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s)
		defs := []params.Definition{{Name: "region", Type: params.Text}, {Name: "min_total", Type: params.Decimal, Default: ptr("50")}}
		in := queries.PreviewInput{
			ConnectionID: e.conn.ID,
			SQL:          "select id, total from orders where created_at >= {{yesterday}} and created_at < {{today}} and region = {{region}} and total >= {{min_total}} order by id",
			Params:       defs,
			Values:       map[string]string{"region": "south"},
		}
		res, err := e.svc.Preview(e.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if res.Timezone != "America/Sao_Paulo" || len(res.Rows) != 1 || res.Rows[0][0] != int64(1) || res.Rows[0][1] != plugin.Decimal("120.5") {
			t.Fatalf("preview: tz=%s rows=%v", res.Timezone, res.Rows)
		}
		got := map[string]string{}
		for _, p := range res.Params {
			got[p.Name] = p.Display
		}
		if got["yesterday"] != "2026-09-24" || got["today"] != "2026-09-25" || got["min_total"] != "50" {
			t.Fatalf("resolved %v", got)
		}

		// The browser time zone decides the day: at 12:00 UTC it is already the 26th in Tokyo.
		in.Timezone = "Asia/Tokyo"
		res, _ = e.svc.Preview(e.ctx, in)
		if res.Timezone != "Asia/Tokyo" || res.Params[1].Display != "2026-09-25" {
			t.Fatalf("tokyo: %+v", res.Params)
		}

		// Values are bound, never interpolated.
		in.Values["region"] = "o'reilly"
		in.Values["min_total"] = "0"
		in.Timezone = "UTC"
		in.SQL = "select id from orders where region = {{region}} and total >= {{min_total}}"
		if res, err := e.svc.Preview(e.ctx, in); err != nil || len(res.Rows) != 1 || res.Rows[0][0] != int64(4) {
			t.Fatalf("quoted value: %+v %v", res, err)
		}

		// Row cap.
		limited := queries.PreviewInput{ConnectionID: e.conn.ID, SQL: "select id from orders", Limit: 2}
		if res, err := e.svc.Preview(e.ctx, limited); err != nil || len(res.Rows) != 2 || !res.Truncated {
			t.Fatalf("limit: %+v %v", res, err)
		}

		for name, bad := range map[string]queries.PreviewInput{
			"bad timezone":    {ConnectionID: e.conn.ID, SQL: "select 1", Timezone: "Mars/Olympus"},
			"limit too large": {ConnectionID: e.conn.ID, SQL: "select 1", Limit: 5000},
			"missing value":   {ConnectionID: e.conn.ID, SQL: "select {{x}}", Params: []params.Definition{{Name: "x", Type: params.Integer}}},
			"bad value":       {ConnectionID: e.conn.ID, SQL: "select {{x}}", Params: []params.Definition{{Name: "x", Type: params.Integer}}, Values: map[string]string{"x": "seven"}},
		} {
			if _, err := e.svc.Preview(e.ctx, bad); err == nil {
				t.Errorf("%s: accepted", name)
			} else if ae, ok := apperr.As(err); !ok || ae.Kind != apperr.KindInvalid {
				t.Errorf("%s: %v", name, err)
			}
		}
		_, err = e.svc.Preview(e.ctx, queries.PreviewInput{ConnectionID: e.conn.ID, SQL: "select * from nope"})
		if ae, ok := apperr.As(err); !ok || ae.Kind != apperr.KindUnprocessable || ae.Code != plugin.ErrCodeQueryFailed {
			t.Fatalf("bad SQL: %v", err)
		}
		_, err = e.svc.Preview(e.ctx, queries.PreviewInput{ConnectionID: e.conn.ID, SQL: "select 1; select 2"})
		if ae, ok := apperr.As(err); !ok || ae.Code != plugin.ErrCodeMultiStatement {
			t.Fatalf("multi-statement: %v", err)
		}
	})
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Vendas por região":        "vendas-por-regiao",
		"  Daily  sales -- 2026! ": "daily-sales-2026",
		"Ação & Reação":            "acao-reacao",
		"日本語":                      "",
	}
	for in, want := range cases {
		got := queries.Slugify(in)
		if want == "" {
			if len(got) != 14 || got[:6] != "query-" {
				t.Errorf("%q: got %q", in, got)
			}
			continue
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
