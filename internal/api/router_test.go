package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/i18n"
	"github.com/rowbird/rowbird/internal/metrics"
	"github.com/rowbird/rowbird/internal/store"
)

type fakeStore struct {
	pingErr   error
	status    store.MigrationStatus
	statusErr error
}

func (f *fakeStore) Ping(context.Context) error { return f.pingErr }
func (f *fakeStore) MigrationStatus(context.Context) (store.MigrationStatus, error) {
	return f.status, f.statusErr
}

func healthyStore() *fakeStore {
	return &fakeStore{status: store.MigrationStatus{Current: 1, Latest: 1}}
}

func do(t *testing.T, h http.Handler, method, path string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) gen.Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type %q, body %s", ct, rec.Body)
	}
	var p gen.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHealthLive(t *testing.T) {
	rec := do(t, NewRouter(Deps{Store: &fakeStore{pingErr: errors.New("down")}}), http.MethodGet, "/health/live")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestHealthReady(t *testing.T) {
	cases := []struct {
		name       string
		store      *fakeStore
		wantCode   int
		wantStore  gen.ComponentStatusStatus
		wantMigrat gen.ComponentStatusStatus
	}{
		{"healthy", healthyStore(), 200, "ok", "ok"},
		{"store down", &fakeStore{pingErr: errors.New("dial tcp 10.0.0.5: connection refused")}, 503, "error", "error"},
		{"pending migrations", &fakeStore{status: store.MigrationStatus{Current: 1, Latest: 2}}, 503, "ok", "error"},
		{"status error", &fakeStore{statusErr: errors.New("boom")}, 503, "ok", "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, NewRouter(Deps{Store: tc.store}), http.MethodGet, "/health/ready")
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d, body %s", rec.Code, rec.Body)
			}
			var body gen.Readiness
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Components["store"].Status != tc.wantStore || body.Components["migrations"].Status != tc.wantMigrat {
				t.Fatalf("components %+v", body.Components)
			}
			if strings.Contains(rec.Body.String(), "10.0.0.5") {
				t.Fatal("readiness body leaks the underlying error")
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := do(t, NewRouter(Deps{Store: healthyStore()}), http.MethodGet, "/health/live")
	for header, want := range map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "script-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be sent over plain HTTP")
	}
}

func TestHSTS(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	cases := []struct {
		name        string
		publicHTTPS bool
		remote      string
		proto       string
		want        bool
	}{
		{"plain http", false, "203.0.113.5:1", "", false},
		{"https base url", true, "203.0.113.5:1", "", true},
		{"trusted proxy says https", false, "10.0.0.2:1", "https", true},
		{"trusted proxy says http", false, "10.0.0.2:1", "http", false},
		{"nearest proxy wins", false, "10.0.0.2:1", "https, http", false},
		{"untrusted peer cannot claim https", false, "203.0.113.5:1", "https", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewRouter(Deps{Store: healthyStore(), TrustedProxies: trusted, SecureCookies: tc.publicHTTPS})
			req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
			req.RemoteAddr = tc.remote
			if tc.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if got := rec.Header().Get("Strict-Transport-Security") != ""; got != tc.want {
				t.Fatalf("HSTS sent = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCORSPreflightIsRefused(t *testing.T) {
	h := NewRouter(Deps{Store: healthyStore()})
	rec := do(t, h, http.MethodOptions, "/api/v1/auth/login", "Origin", "https://evil.example", "Access-Control-Request-Method", "POST")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/health/live", "Origin", "https://evil.example")
	for k := range rec.Header() {
		if strings.HasPrefix(k, "Access-Control-") {
			t.Fatalf("CORS header %s sent", k)
		}
	}
}

func TestRequestID(t *testing.T) {
	h := NewRouter(Deps{Store: healthyStore()})
	generated := do(t, h, http.MethodGet, "/health/live").Header().Get(RequestIDHeader)
	if len(generated) != 36 {
		t.Fatalf("generated id %q", generated)
	}
	if got := do(t, h, http.MethodGet, "/health/live", RequestIDHeader, "proxy-abc.123").Header().Get(RequestIDHeader); got != "proxy-abc.123" {
		t.Fatalf("incoming id not reused: %q", got)
	}
	if got := do(t, h, http.MethodGet, "/health/live", RequestIDHeader, "bad id\n<script>").Header().Get(RequestIDHeader); got == "bad id\n<script>" || len(got) != 36 {
		t.Fatalf("malformed incoming id accepted: %q", got)
	}
	rec := do(t, h, http.MethodGet, "/api/v1/nope", RequestIDHeader, "trace-1")
	if p := decodeProblem(t, rec); p.RequestId == nil || *p.RequestId != "trace-1" {
		t.Fatalf("problem lacks request id: %+v", p)
	}
}

func TestUnknownAPIRoutesAreProblems(t *testing.T) {
	spa := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("spa")) })
	h := NewRouter(Deps{Store: healthyStore(), SPA: spa})

	for _, path := range []string{"/api/v1/does-not-exist", "/api", "/health/nope", "/metrics"} {
		rec := do(t, h, http.MethodGet, path)
		p := decodeProblem(t, rec)
		if rec.Code != 404 || p.Code != CodeRouteNotFound || p.I18nKey != "errors.route.not_found" ||
			p.Type != "https://rowbird.dev/errors/route.not_found" || p.Instance == nil || *p.Instance != path {
			t.Errorf("%s: %d %+v", path, rec.Code, p)
		}
	}
	for _, path := range []string{"/", "/reports/123", "/apiary"} {
		if rec := do(t, h, http.MethodGet, path); rec.Body.String() != "spa" {
			t.Errorf("%s was not served by the SPA: %s", path, rec.Body)
		}
	}
	rec := do(t, h, http.MethodPost, "/health/live")
	if p := decodeProblem(t, rec); rec.Code != 405 || p.Code != CodeMethodNotAllowed {
		t.Errorf("POST /health/live: %d %+v", rec.Code, p)
	}
}

func TestProblemIsLocalized(t *testing.T) {
	h := NewRouter(Deps{Store: healthyStore()})
	en := decodeProblem(t, do(t, h, http.MethodGet, "/api/x"))
	pt := decodeProblem(t, do(t, h, http.MethodGet, "/api/x", "Accept-Language", "pt-BR,pt;q=0.9"))
	if en.Title == pt.Title || pt.Title != "Não existe endpoint da API neste caminho" {
		t.Fatalf("en %q, pt %q", en.Title, pt.Title)
	}
	if en.Code != pt.Code || en.I18nKey != pt.I18nKey {
		t.Fatal("code and i18n key must not depend on the locale")
	}
}

func TestPanicBecomesProblemWithoutDetails(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	problems := &problemWriter{bundle: i18n.Default(), logger: logger}
	h := requestID(recoverer(logger, problems)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("secret internal state")
	})))

	rec := do(t, h, http.MethodGet, "/api/v1/boom")
	p := decodeProblem(t, rec)
	if rec.Code != 500 || p.Code != CodeInternal {
		t.Fatalf("%d %+v", rec.Code, p)
	}
	if strings.Contains(rec.Body.String(), "secret internal state") || strings.Contains(rec.Body.String(), "goroutine") {
		t.Fatalf("panic details leaked to the client: %s", rec.Body)
	}
	if !strings.Contains(logs.String(), "secret internal state") || !strings.Contains(logs.String(), "stack") {
		t.Fatalf("panic was not logged: %s", logs.String())
	}
}

func TestDomainErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{store.ErrNotFound, 404, CodeNotFound},
		{fmt.Errorf("get report: %w", store.ErrNotFound), 404, CodeNotFound},
		{store.ErrConflict, 409, CodeConflictVersion},
		{errors.Join(store.ErrDuplicate, errors.New("UNIQUE constraint failed")), 409, CodeConflictDup},
		{&Error{Status: 422, Code: "connection.auth_failed"}, 422, "connection.auth_failed"},
		{errors.New("password=hunter2 rejected"), 500, CodeInternal},
	}
	for _, tc := range cases {
		var logs bytes.Buffer
		pw := &problemWriter{bundle: i18n.Default(), logger: slog.New(slog.NewTextHandler(&logs, nil))}
		rec := httptest.NewRecorder()
		pw.writeError(rec, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil), tc.err)
		p := decodeProblem(t, rec)
		if rec.Code != tc.status || p.Code != tc.code || p.Status != tc.status {
			t.Errorf("%v: got %d %s", tc.err, rec.Code, p.Code)
		}
		if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "UNIQUE") {
			t.Errorf("%v: internal error text leaked: %s", tc.err, rec.Body)
		}
	}
}

func TestAccessLogOmitsQueryString(t *testing.T) {
	var logs bytes.Buffer
	// Debug level, because successful probes are logged there.
	h := NewRouter(Deps{Store: healthyStore(), Logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	do(t, h, http.MethodGet, "/health/live?token=abc123")
	out := logs.String()
	if !strings.Contains(out, `"path":"/health/live"`) || !strings.Contains(out, `"status":200`) {
		t.Fatalf("access log %s", out)
	}
	if strings.Contains(out, "abc123") {
		t.Fatal("access log contains the query string")
	}
}

type fakeScheduler bool

func (f fakeScheduler) Healthy() bool { return bool(f) }

func TestHealthReadyScheduler(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sched SchedulerHealth
		code  int
		want  gen.ComponentStatusStatus
	}{
		{"ticking", fakeScheduler(true), 200, "ok"},
		{"stuck", fakeScheduler(false), 503, "error"},
		{"no scheduler here", nil, 200, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, NewRouter(Deps{Store: healthyStore(), Scheduler: tc.sched}), http.MethodGet, "/health/ready")
			var body gen.Readiness
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if rec.Code != tc.code || body.Components["scheduler"].Status != tc.want {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestMetricsRoute(t *testing.T) {
	m := metrics.New(fakeSource{}, "dev", nil)
	h := NewRouter(Deps{Store: healthyStore(), Metrics: m, MetricsToken: "tok"})
	if rec := do(t, h, http.MethodGet, "/metrics"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without token: %d", rec.Code)
	}
	rec := do(t, h, http.MethodGet, "/metrics", "Authorization", "Bearer tok")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "rowbird_build_info") {
		t.Fatalf("with token: %d", rec.Code)
	}
	if rec := do(t, NewRouter(Deps{Store: healthyStore()}), http.MethodGet, "/metrics"); rec.Code != http.StatusNotFound {
		t.Fatalf("metrics disabled: %d", rec.Code)
	}
}

type fakeSource struct{}

func (fakeSource) CountPendingRuns(context.Context) (int, error) { return 0, nil }
func (fakeSource) ArtifactBytes(context.Context) (int64, error)  { return 0, nil }
