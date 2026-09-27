package connections_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/connections"
	_ "github.com/rowbird/rowbird/internal/connector/sqlite"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/logging"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

// fakeDB records the values it is opened with and fails when asked to.
type fakeDB struct{}

var (
	fakeMu     sync.Mutex
	fakeOpened []map[string]any
)

func (fakeDB) Meta() plugin.Metadata {
	return plugin.Metadata{ID: "fakedb", Name: "plugin.fakedb.name"}
}

func (fakeDB) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "host", Type: plugin.TypeString, Required: true, Label: "l"},
		{Key: "port", Type: plugin.TypeInteger, Default: 1234, Label: "l"},
		{Key: "password", Type: plugin.TypeString, Secret: true, Label: "l"},
		{Key: "token", Type: plugin.TypeString, Secret: true, Label: "l"},
	}}
}
func (fakeDB) Capabilities() any         { return plugin.ConnectorCapabilities{} }
func (fakeDB) Messages() plugin.Messages { return nil }
func (fakeDB) Open(_ context.Context, opts plugin.OpenOptions) (plugin.Conn, error) {
	fakeMu.Lock()
	fakeOpened = append(fakeOpened, opts.Values)
	fakeMu.Unlock()
	if opts.Values["host"] == "ssh-unknown" {
		return nil, &plugin.ConnError{Code: plugin.ErrCodeSSHHostKeyUnknown, Detail: map[string]string{"fingerprint": "SHA256:abc"}, Err: errors.New("driver said pw " + str(opts.Values["password"]))}
	}
	return fakeConn{}, nil
}

func str(v any) string { s, _ := v.(string); return s }

type fakeConn struct{}

func (fakeConn) Ping(context.Context) (plugin.ServerInfo, error) {
	return plugin.ServerInfo{Version: "Fake 1"}, nil
}

func (fakeConn) Schema(context.Context) (*plugin.DBSchema, error) {
	return &plugin.DBSchema{Tables: []plugin.Table{{Name: "t", Kind: "table"}}}, nil
}
func (fakeConn) CanWrite(context.Context) (*bool, error) { yes := true; return &yes, nil }
func (fakeConn) Query(context.Context, plugin.BoundQuery, plugin.QueryOptions) (plugin.RowStream, error) {
	return nil, errors.New("not used")
}
func (fakeConn) Close() error { return nil }

func init() {
	plugin.Register(plugin.KindConnector, "fakedb", func() plugin.Plugin { return fakeDB{} })
}

func lastOpened() map[string]any {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	return fakeOpened[len(fakeOpened)-1]
}

type env struct {
	svc  *connections.Service
	st   *store.Store
	ctx  context.Context
	p    *auth.Principal
	dir  string
	logs *bytes.Buffer
}

func newEnv(t *testing.T, s *store.Store) *env {
	t.Helper()
	w := &store.Workspace{Name: "w", Slug: "w"}
	if err := s.Workspaces().Create(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	kr, _ := crypto.NewKeyring(key)
	dir := t.TempDir()
	var logs bytes.Buffer
	logger, _ := logging.New(&logs, "debug", "json")
	svc := connections.NewService(s, kr, connections.Options{
		Dial: netx.NewDialer(netx.PolicyOpen).DialContext, SQLiteDirs: []string{dir}, Logger: logger,
	})
	u := &store.User{Email: "a@example.com", Name: "A", Locale: "en", Theme: "system"}
	_ = s.Users().Create(t.Context(), u)
	ctx := store.WithWorkspace(t.Context(), w.ID)
	return &env{svc: svc, st: s, ctx: ctx, p: &auth.Principal{UserID: u.ID, WorkspaceID: w.ID, Role: store.RoleAdmin}, dir: dir, logs: &logs}
}

func (e *env) sqliteFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(e.dir, name)
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE orders (id INTEGER, total DECIMAL(10,2))"); err != nil {
		t.Fatal(err)
	}
	return p
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

func TestCreateSQLite(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s)
		path := e.sqliteFile(t, "shop.db")

		_, err := e.svc.Create(e.ctx, e.p, connections.Input{Name: "Bad Name!", Driver: "sqlite", Config: map[string]any{}}, auth.RequestMeta{})
		f := fields(t, err)
		if f["name"] != "validation.slug" || f["config.path"] != "validation.required" {
			t.Fatalf("fields %v", f)
		}
		_, err = e.svc.Create(e.ctx, e.p, connections.Input{Name: "x", Driver: "oracle", Config: map[string]any{}}, auth.RequestMeta{})
		if fields(t, err)["driver"] != "validation.invalid_value" {
			t.Fatal("unknown driver accepted")
		}

		v, err := e.svc.Create(e.ctx, e.p, connections.Input{Name: "shop", Driver: "sqlite", Config: map[string]any{"path": path}}, auth.RequestMeta{IP: "1.2.3.4"})
		if err != nil {
			t.Fatal(err)
		}
		if v.Status != store.ConnectionOK || !strings.HasPrefix(v.ServerVersion, "SQLite") || v.HasWritePermission == nil || *v.HasWritePermission {
			t.Fatalf("created %+v", v.Connection)
		}
		schema, at, err := e.svc.Schema(e.ctx, v.ID)
		if err != nil || at == nil || len(schema.Tables) != 1 || schema.Tables[0].Columns[1].Type != plugin.TypeDecimal {
			t.Fatalf("schema %+v %v %v", schema, at, err)
		}

		_, err = e.svc.Create(e.ctx, e.p, connections.Input{Name: "shop", Driver: "sqlite", Config: map[string]any{"path": path}}, auth.RequestMeta{})
		if !errors.Is(err, connections.ErrNameTaken) {
			t.Fatalf("duplicate: %v", err)
		}

		// A connection that fails its check is still saved, with the error recorded.
		outside := filepath.Join(t.TempDir(), "x.db")
		_ = os.WriteFile(outside, nil, 0o600)
		bad, err := e.svc.Create(e.ctx, e.p, connections.Input{Name: "outside", Driver: "sqlite", Config: map[string]any{"path": outside}}, auth.RequestMeta{})
		if err != nil || bad.Status != store.ConnectionError || bad.LastError != plugin.ErrCodePathNotAllowed {
			t.Fatalf("failing connection: %+v %v", bad, err)
		}
		_, _, err = e.svc.RefreshSchema(e.ctx, bad.ID)
		if ae, ok := apperr.As(err); !ok || ae.Kind != apperr.KindUnprocessable || ae.Code != plugin.ErrCodePathNotAllowed {
			t.Fatalf("refresh of failing connection: %v", err)
		}

		limit := 0
		_, err = e.svc.Update(e.ctx, e.p, v.ID, connections.Patch{Version: v.Version, MaxRows: &limit}, auth.RequestMeta{})
		if fields(t, err)["max_rows"] != plugin.CodeOutOfRange {
			t.Fatal("max_rows 0 accepted")
		}
		excluded := []string{" main.salaries ", "main.salaries", "", "audit"}
		upd, err := e.svc.Update(e.ctx, e.p, v.ID, connections.Patch{Version: v.Version, AIExcludedTables: &excluded}, auth.RequestMeta{})
		if err != nil || strings.Join(upd.AIExcludedTables, ",") != "audit,main.salaries" {
			t.Fatalf("excluded tables %v %v", upd, err)
		}
		if _, err := e.svc.Update(e.ctx, e.p, v.ID, connections.Patch{Version: v.Version}, auth.RequestMeta{}); !errors.Is(err, store.ErrConflict) {
			t.Fatalf("stale version: %v", err)
		}

		if err := e.svc.Delete(e.ctx, e.p, v.ID, auth.RequestMeta{}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Get(e.ctx, v.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("not deleted")
		}
	})
}

func TestSecrets(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s)
		const pw1, pw2, tok = "first-secret-pw", "second-secret-pw", "api-token-value"

		v, err := e.svc.Create(e.ctx, e.p, connections.Input{
			Name: "fake", Driver: "fakedb",
			Config: map[string]any{"host": "db", "password": pw1, "token": tok},
		}, auth.RequestMeta{})
		if err != nil {
			t.Fatal(err)
		}
		if lastOpened()["password"] != pw1 || lastOpened()["port"] != int64(1234) {
			t.Fatalf("opened with %v", lastOpened())
		}
		if _, leaked := v.Config["password"]; leaked || !v.SecretsConfigured["password"] || !v.SecretsConfigured["token"] {
			t.Fatalf("view %+v %v", v.Config, v.SecretsConfigured)
		}
		stored, _ := s.Connections().Get(e.ctx, v.ID)
		if stored.SecretsEnc == nil || strings.Contains(*stored.SecretsEnc, pw1) || strings.Contains(string(mustJSON(stored.Config)), pw1) {
			t.Fatal("secret stored in clear")
		}

		placeholder := map[string]any{"configured": true}
		// Placeholder keeps, absent keeps, null clears.
		v, err = e.svc.Update(e.ctx, e.p, v.ID, connections.Patch{Version: v.Version, Config: map[string]any{"host": "db2", "password": placeholder, "token": nil}}, auth.RequestMeta{})
		if err != nil {
			t.Fatal(err)
		}
		if lastOpened()["password"] != pw1 || lastOpened()["token"] != nil || lastOpened()["host"] != "db2" || v.SecretsConfigured["token"] {
			t.Fatalf("after placeholder update: %v %v", lastOpened(), v.SecretsConfigured)
		}
		v, err = e.svc.Update(e.ctx, e.p, v.ID, connections.Patch{Version: v.Version, Config: map[string]any{"host": "db2", "password": pw2}}, auth.RequestMeta{})
		if err != nil || lastOpened()["password"] != pw2 {
			t.Fatalf("replace: %v %v", lastOpened(), err)
		}

		// Testing an edited form reuses the saved secret for placeholders.
		res, err := e.svc.Test(e.ctx, connections.TestInput{ConnectionID: &v.ID, Driver: "fakedb", Config: map[string]any{"host": "db3", "password": placeholder}})
		if err != nil || !res.OK || lastOpened()["password"] != pw2 || res.ServerVersion != "Fake 1" {
			t.Fatalf("test with saved secret: %+v %v %v", res, lastOpened(), err)
		}
		// Unsaved test without a connection: the placeholder means no password.
		if _, err := e.svc.Test(e.ctx, connections.TestInput{Driver: "fakedb", Config: map[string]any{"host": "db3", "password": placeholder}}); err != nil || lastOpened()["password"] != nil {
			t.Fatalf("placeholder without connection: %v %v", lastOpened(), err)
		}
		res, _ = e.svc.Test(e.ctx, connections.TestInput{Driver: "fakedb", Config: map[string]any{"host": "ssh-unknown", "password": pw1}})
		if res.OK || res.ErrorCode != plugin.ErrCodeSSHHostKeyUnknown || res.ErrorDetail["fingerprint"] != "SHA256:abc" {
			t.Fatalf("failure result %+v", res)
		}
		if _, err := e.svc.Update(e.ctx, e.p, v.ID, connections.Patch{Version: v.Version, Config: map[string]any{"driver": "sqlite"}}, auth.RequestMeta{}); err == nil {
			t.Fatal("driver change accepted")
		}

		// No secret reaches logs, events or views.
		events, _ := s.SecurityEvents().List(e.ctx, store.SecurityEventFilter{}, store.PageRequest{Limit: 100})
		list, _ := e.svc.List(e.ctx)
		for where, text := range map[string]string{"logs": e.logs.String(), "events": string(mustJSON(events.Items)), "views": string(mustJSON(list))} {
			for _, secret := range []string{pw1, pw2, tok} {
				if strings.Contains(text, secret) {
					t.Errorf("%s contain a secret", where)
				}
			}
		}
		if len(events.Items) != 3 {
			t.Fatalf("expected create and two update events, got %d", len(events.Items))
		}
	})
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestSafeMessage(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s)
		const pw = "hunter2-secret"
		v, err := e.svc.Create(e.ctx, e.p, connections.Input{Name: "fake", Driver: "fakedb", Config: map[string]any{"host": "db", "password": pw}}, auth.RequestMeta{})
		if err != nil {
			t.Fatal(err)
		}
		driverErr := plugin.NewConnError(plugin.ErrCodeQueryFailed, errors.New(`login for "app" with password `+pw+` failed near "selec"`))
		got := e.svc.SafeMessage(e.ctx, v.ID, driverErr)
		if strings.Contains(got, pw) || !strings.Contains(got, `near "selec"`) {
			t.Errorf("message %q", got)
		}
		if e.svc.SafeMessage(e.ctx, v.ID, errors.New("plain")) != "" || e.svc.SafeMessage(e.ctx, v.ID, plugin.NewConnError("x", nil)) != "" {
			t.Error("messages without a driver error")
		}
	})
}
