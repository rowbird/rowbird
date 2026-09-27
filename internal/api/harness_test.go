package api

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/rowbird/rowbird/internal/ai"
	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/connections"
	_ "github.com/rowbird/rowbird/internal/connector/sqlite"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/gitops"
	"github.com/rowbird/rowbird/internal/links"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/notify"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/aitest"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/reports"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

// testAI answers every test server's AI requests.
var testAI = aitest.NewFake()

const testAdminPassword = "admin passphrase 1"

// testServer is a router backed by a real SQLite store.
type testServer struct {
	t         *testing.T
	store     *store.Store
	svc       *auth.Service
	handler   http.Handler
	sqliteDir string
}

func newTestServer(t *testing.T, mutate ...func(*Deps)) *testServer {
	t.Helper()
	return newTestServerWith(t, auth.Config{}, mutate...)
}

// newTestServerWith is newTestServer with an identity configuration (a base URL for passkeys).
func newTestServerWith(t *testing.T, authCfg auth.Config, mutate ...func(*Deps)) *testServer {
	t.Helper()
	st := storetest.Open(t, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "api.db")))
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	kr, _ := crypto.NewKeyring(key)
	svc := auth.NewService(st, kr, authCfg, auth.Options{
		Hasher: auth.NewPasswordHasher(auth.Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, 4),
	})
	sqliteDir := t.TempDir()
	conns := connections.NewService(st, kr, connections.Options{
		Dial: netx.NewDialer(netx.PolicyOpen).DialContext, SQLiteDirs: []string{sqliteDir},
	})
	qs := queries.NewService(st, conns, queries.Options{})
	chs := channels.NewService(st, kr, channels.Options{})
	rps := reports.NewService(st, qs, reports.Options{})
	d := Deps{
		Store: st, Auth: svc, Connections: conns, Queries: qs, Reports: rps,
		Planner: gitops.NewPlanner(st, conns, chs, qs, rps), Exporter: gitops.NewExporter(st, conns, chs),
		Channels: chs,
		Notify:   notify.New(st, kr, chs, notify.Options{Synchronous: true}),
		Links:    links.NewService(st, nil, links.Options{}),
		AI: ai.NewService(st, kr, conns, ai.Options{Provider: func(id string) (plugin.AIProvider, bool) {
			return testAI, id == aitest.FakeID
		}}),
		MasterKey:      MasterKeyInfo{Source: "generated", Path: "/data/master.key"},
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
		System:         &SystemInfo{Store: st, StorageBackend: "local"},
	}
	for _, m := range mutate {
		m(&d)
	}
	return &testServer{t: t, store: st, svc: svc, handler: NewRouter(d), sqliteDir: sqliteDir}
}

// client carries cookies and headers between requests, like a browser tab or a script.
type client struct {
	ts      *testServer
	cookies map[string]*http.Cookie
	bearer  string
	remote  string
	headers map[string]string
}

func (ts *testServer) client() *client {
	return &client{ts: ts, cookies: map[string]*http.Cookie{}, remote: "198.51.100.10:5000", headers: map[string]string{}}
}

type response struct {
	*httptest.ResponseRecorder
}

func (r response) problem(t *testing.T) gen.Problem {
	t.Helper()
	var p gen.Problem
	if err := json.Unmarshal(r.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem: %d %s", r.Code, r.Body)
	}
	return p
}

func (r response) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %d %s: %v", r.Code, r.Body, err)
	}
}

func (r response) cookie(name string) *http.Cookie {
	for _, c := range r.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// do sends a request. A non-nil body is sent as JSON. The CSRF header is added automatically
// from the cookie, like the SPA does, unless noCSRF is set.
func (c *client) do(method, path string, body any, opts ...func(*http.Request)) response {
	c.ts.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.ts.t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	var req *http.Request
	if reader != nil {
		req = httptest.NewRequest(method, path, reader)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.RemoteAddr = c.remote
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	if ck, ok := c.cookies[CSRFCookie]; ok {
		req.Header.Set(CSRFHeader, ck.Value)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	c.ts.handler.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck
		}
	}
	return response{rec}
}

// setup runs the wizard over HTTP and returns the signed-in admin client.
func (ts *testServer) setup() *client {
	ts.t.Helper()
	c := ts.client()
	res := c.do(http.MethodPost, "/api/v1/setup", map[string]any{
		"email": "admin@example.com", "name": "Admin", "password": testAdminPassword, "locale": "en", "timezone": "UTC",
	})
	if res.Code != http.StatusCreated {
		ts.t.Fatalf("setup: %d %s", res.Code, res.Body)
	}
	return c
}

// signIn logs in over HTTP (no second factor).
func (ts *testServer) signIn(email, password string) *client {
	ts.t.Helper()
	c := ts.client()
	res := c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": email, "password": password})
	var body gen.LoginResponse
	res.decode(ts.t, &body)
	if res.Code != http.StatusOK || body.Status != gen.Authenticated {
		ts.t.Fatalf("login %s: %d %s", email, res.Code, res.Body)
	}
	return c
}

// createUser adds a user through the API and returns the temporary password.
func (c *client) createUser(email, role string) (gen.User, string) {
	c.ts.t.Helper()
	res := c.do(http.MethodPost, "/api/v1/users", map[string]any{"email": email, "name": "User", "role": role})
	if res.Code != http.StatusCreated {
		c.ts.t.Fatalf("create user: %d %s", res.Code, res.Body)
	}
	var out gen.UserCreated
	res.decode(c.ts.t, &out)
	return out.User, out.TemporaryPassword
}

// activate signs in with the temporary password and changes it, returning a normal session.
func (ts *testServer) activate(email, temp string) *client {
	ts.t.Helper()
	c := ts.signIn(email, temp)
	res := c.do(http.MethodPost, "/api/v1/me/password", map[string]any{"current_password": temp, "new_password": "fresh passphrase 42"})
	if res.Code != http.StatusNoContent {
		ts.t.Fatalf("change password: %d %s", res.Code, res.Body)
	}
	return c
}

func (c *client) createKey(scopes ...string) string {
	c.ts.t.Helper()
	res := c.do(http.MethodPost, "/api/v1/api-keys", map[string]any{"name": "key", "scopes": scopes})
	if res.Code != http.StatusCreated {
		c.ts.t.Fatalf("create key: %d %s", res.Code, res.Body)
	}
	var out gen.ApiKeyCreated
	res.decode(c.ts.t, &out)
	return out.Key
}
