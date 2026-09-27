// Package api implements the HTTP API defined in api/openapi.yaml on top of the generated
// interfaces in internal/api/gen.
package api

import (
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/rowbird/rowbird/internal/gitops"

	"github.com/rowbird/rowbird/internal/ai"

	"github.com/go-chi/chi/v5"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/connections"
	"github.com/rowbird/rowbird/internal/i18n"
	"github.com/rowbird/rowbird/internal/links"
	"github.com/rowbird/rowbird/internal/metrics"
	"github.com/rowbird/rowbird/internal/notify"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/reports"
)

// Deps are the collaborators of the HTTP layer.
type Deps struct {
	Logger *slog.Logger
	Store  StoreHealth
	Auth   *auth.Service
	// Connections manages user databases.
	Connections *connections.Service
	// Queries manages versioned SQL and previews.
	Queries *queries.Service
	// Reports manages reports, schedules and runs.
	Reports *reports.Service
	// Channels manages destinations and their health.
	Channels *channels.Service
	// Links serves shared links.
	Links *links.Service
	// Notify keeps notifications, system alert settings and real-time events.
	Notify *notify.Service
	// AI answers assistant requests and keeps the AI settings.
	AI *ai.Service
	// Planner and Exporter are configuration as code (import, export, GitOps detach).
	Planner  *gitops.Planner
	Exporter *gitops.Exporter
	// Metrics is served at /metrics, behind MetricsToken when it is set. Nil serves nothing.
	Metrics      *metrics.Metrics
	MetricsToken string
	// Scheduler is reported by the readiness probe; nil on instances without one.
	Scheduler SchedulerHealth
	// MasterKey is reported by the setup wizard.
	MasterKey MasterKeyInfo
	// TrustedProxies may set X-Forwarded-For and X-Forwarded-Proto.
	TrustedProxies []netip.Prefix
	// SecureCookies marks cookies Secure; true when the public base URL is https.
	SecureCookies bool
	// System reports on storage and backups.
	System *SystemInfo
	// SPA serves the web UI for every path that is not an API or health route. Nil disables it.
	SPA http.Handler
}

// Login throttling per client IP (docs/spec/07-security.md). Generous on purpose: several users
// behind one NAT must not lock each other out. Account lockout is the real brute-force defense.
const (
	loginBurst  = 30
	loginWindow = 10 * time.Minute
)

// server implements gen.StrictServerInterface by embedding one handler group per area.
type server struct {
	*healthHandlers
	*authHandlers
	*meHandlers
	*adminHandlers
	*connectionHandlers
	*queryHandlers
	*reportHandlers
	*channelHandlers
	*linkHandlers
	*notifyHandlers
	*aiHandlers
	*configHandlers
	*systemHandlers
}

var _ gen.StrictServerInterface = (*server)(nil)

// NewRouter builds the root HTTP handler. It panics if the embedded OpenAPI document declares an
// operation without an authorization policy, which tests catch before release.
func NewRouter(d Deps) http.Handler {
	logger := d.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	table, err := loadPolicies()
	if err != nil {
		panic(err)
	}
	problems := &problemWriter{bundle: i18n.Default(), logger: logger}
	cookies := cookieWriter{secure: d.SecureCookies}

	r := chi.NewRouter()
	r.Use(requestID, clientIP(d.TrustedProxies), securityHeaders(d.SecureCookies), noCORS, accessLog(logger), recoverer(logger, problems))
	if d.Auth != nil {
		r.Use(onlyAPI(authenticate(d.Auth, problems)), onlyAPI(csrfProtect(d.Auth, problems)))
	}

	badRequest := func(w http.ResponseWriter, r *http.Request, err error) {
		problems.write(w, r, &Error{Status: http.StatusBadRequest, Code: CodeRequestInvalid, Detail: err.Error(), Err: err})
	}
	impl := &server{
		healthHandlers:     &healthHandlers{store: d.Store, scheduler: d.Scheduler, logger: logger},
		authHandlers:       &authHandlers{svc: d.Auth, cookies: cookies, masterKey: d.MasterKey},
		meHandlers:         &meHandlers{svc: d.Auth},
		adminHandlers:      &adminHandlers{svc: d.Auth, cookies: cookies, linksEnabled: d.Reports != nil && d.Reports.LinksEnabled()},
		connectionHandlers: &connectionHandlers{svc: d.Connections},
		queryHandlers:      &queryHandlers{svc: d.Queries},
		reportHandlers:     &reportHandlers{svc: d.Reports},
		channelHandlers:    &channelHandlers{svc: d.Channels, reports: d.Reports, auth: d.Auth},
		linkHandlers:       &linkHandlers{svc: d.Links},
		notifyHandlers:     &notifyHandlers{svc: d.Notify},
		configHandlers:     &configHandlers{planner: d.Planner, exporter: d.Exporter},
		systemHandlers:     &systemHandlers{info: d.System},
		aiHandlers:         &aiHandlers{svc: d.AI, auth: d.Auth, limiter: newRateLimiter(aiBurst, aiWindow, nil)},
	}
	strict := gen.NewStrictHandlerWithOptions(
		impl,
		[]gen.StrictMiddlewareFunc{authorize(table, newRateLimiter(loginBurst, loginWindow, nil))},
		gen.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  badRequest,
			ResponseErrorHandlerFunc: problems.writeError,
		},
	)
	gen.HandlerWithOptions(strict, gen.ChiServerOptions{BaseRouter: r, ErrorHandlerFunc: badRequest})
	if d.Metrics != nil {
		r.Method(http.MethodGet, "/metrics", d.Metrics.Handler(d.MetricsToken))
	}
	if d.Links != nil {
		r.Get("/r/{token}", linkDownload(d.Links, d.Auth, newRateLimiter(linkBurst, linkWindow, nil)))
	}

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if isAPIPath(req.URL.Path) || d.SPA == nil {
			problems.write(w, req, &Error{Status: http.StatusNotFound, Code: CodeRouteNotFound})
			return
		}
		d.SPA.ServeHTTP(w, req)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		problems.write(w, req, &Error{Status: http.StatusMethodNotAllowed, Code: CodeMethodNotAllowed})
	})
	return r
}

// onlyAPI applies mw to /api/ requests only, so static assets and health probes never touch the
// session store.
func onlyAPI(mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		wrapped := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				wrapped.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isAPIPath reports whether a path belongs to the API rather than to the SPA.
func isAPIPath(p string) bool {
	for _, prefix := range []string{"/api", "/health", "/metrics"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}
