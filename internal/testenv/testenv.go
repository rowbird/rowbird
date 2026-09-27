// Package testenv builds a workspace with a SQLite "shop" database, a connection to it and the
// services on top, for tests of the packages that run queries. It is only imported by tests.
package testenv

import (
	"context"
	"crypto/rand"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/connections"
	_ "github.com/rowbird/rowbird/internal/connector/sqlite" // the shop connection
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/params"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

// Timezone is the workspace default time zone.
const Timezone = "America/Sao_Paulo"

// Clock is a settable clock, safe for concurrent use.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

// NewClock starts a clock at t.
func NewClock(t time.Time) *Clock { return &Clock{t: t} }

// Now returns the current time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Set moves the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// Env is a workspace ready to run queries.
type Env struct {
	Store     *store.Store
	Ctx       context.Context
	Principal *auth.Principal
	Conns     *connections.Service
	Queries   *queries.Service
	Conn      *connections.View
	// ShopPath is the SQLite file behind Conn; Dir is its directory.
	ShopPath, Dir string
	Clock         *Clock
	// Keyring encrypts secrets, for services built on top of the environment.
	Keyring *crypto.Keyring
}

// ShopSQL creates the shop fixture: four orders around 2026-09-25.
const ShopSQL = `CREATE TABLE orders (id INTEGER, region TEXT, total DECIMAL(10,2), created_at DATETIME);
INSERT INTO orders VALUES (1, 'south', 120.5, '2026-09-24 10:00:00'), (2, 'north', 80, '2026-09-25 09:00:00'),
	(3, 'south', 10, '2026-09-20 09:00:00'), (4, 'o''reilly', 1, '2026-09-25 09:00:00');`

// New builds the environment on a fresh SQLite store. The clock starts at 2026-09-25 12:00 UTC
// (09:00 in São Paulo).
func New(t *testing.T) *Env {
	t.Helper()
	return NewOn(t, "sqlite://"+filepath.Join(t.TempDir(), "rowbird.db"))
}

// NewOn builds the environment on an empty store at url (storetest.Dialects gives one per
// dialect).
func NewOn(t *testing.T, url string) *Env {
	t.Helper()
	s := storetest.Open(t, url)
	if _, err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := &store.Workspace{Name: "w", Slug: "w"}
	if err := s.Workspaces().Create(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	u := &store.User{Email: "ana@example.com", Name: "Ana", Locale: "en", Theme: "system"}
	if err := s.Users().Create(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	ctx := store.WithWorkspace(context.Background(), w.ID)
	if err := s.Settings().Put(ctx, auth.SettingDefaultTimezone, `"`+Timezone+`"`, false); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	kr, _ := crypto.NewKeyring(key)
	dir := t.TempDir()
	path := filepath.Join(dir, "shop.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(t.Context(), ShopSQL)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	clock := NewClock(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	conns := connections.NewService(s, kr, connections.Options{Dial: netx.NewDialer(netx.PolicyOpen).DialContext, SQLiteDirs: []string{dir}})
	p := &auth.Principal{UserID: u.ID, WorkspaceID: w.ID, Role: store.RoleAdmin}
	conn, err := conns.Create(ctx, p, connections.Input{Name: "shop", Driver: "sqlite", Config: map[string]any{"path": path}}, auth.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	return &Env{
		Store: s, Ctx: ctx, Principal: p, Conns: conns, Conn: conn, ShopPath: path, Dir: dir, Clock: clock, Keyring: kr,
		Queries: queries.NewService(s, conns, queries.Options{Now: clock.Now}),
	}
}

// Query saves a query on the shop connection.
func (e *Env) Query(t *testing.T, slug, sql string, defs ...params.Definition) *queries.View {
	t.Helper()
	v, err := e.Queries.Create(e.Ctx, e.Principal, queries.Input{Title: slug, Slug: slug, ConnectionID: e.Conn.ID, SQL: sql, Params: defs})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
