package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/rowbird/rowbird/internal/store/ids"
)

// RequestIDHeader carries the request id in both directions.
const RequestIDHeader = "X-Request-ID"

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._\-]{1,128}$`)

type requestIDKey struct{}

// RequestIDFrom returns the id assigned to the request by the requestID middleware.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// requestID reuses a well-formed incoming X-Request-ID (so ids from a reverse proxy correlate)
// and otherwise generates one. The id is echoed in the response.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID.MatchString(id) {
			id = ids.New().String()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// accessLog logs one line per request. The query string is left out because it may carry tokens.
// Successful health probes and metrics scrapes, which arrive every few seconds, are logged at debug.
func accessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelInfo
			switch {
			case status >= http.StatusInternalServerError:
				level = slog.LevelError
			case status < http.StatusBadRequest && isProbe(r.URL.Path):
				level = slog.LevelDebug
			}
			logger.Log(r.Context(), level, "http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFrom(r.Context()),
			)
		})
	}
}

func isProbe(path string) bool {
	return strings.HasPrefix(path, "/health/") || path == "/metrics"
}

// recoverer turns a panic into a 500 problem. The stack goes to the log, never to the client.
func recoverer(logger *slog.Logger, problems *problemWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler { //nolint:errorlint // sentinel comparison as documented by net/http
					panic(rec)
				}
				logger.ErrorContext(r.Context(), "panic while serving request",
					"panic", fmt.Sprint(rec), "stack", string(debug.Stack()),
					"request_id", RequestIDFrom(r.Context()))
				problems.write(w, r, &Error{Status: http.StatusInternalServerError, Code: CodeInternal})
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// contentSecurityPolicy allows only same-origin resources. Inline styles are permitted because
// the component library sets style attributes at runtime; scripts are never inline.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; " +
	"base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// securityHeaders sets the baseline hardening headers (docs/spec/07-security.md). HSTS is sent when
// the client reached us over HTTPS (directly or through a trusted proxy) or when the public base
// URL is https, since browsers ignore it on plain HTTP anyway.
func securityHeaders(publicHTTPS bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", contentSecurityPolicy)
			h.Set("X-Frame-Options", "DENY")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			if publicHTTPS || RequestIsHTTPS(r.Context()) {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// noCORS answers CORS preflights with 403. The UI is served from the same origin and never sends
// one, and no response carries Access-Control-* headers, so browsers keep other origins out.
func noCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions && r.Header.Get("Origin") != "" && r.Header.Get("Access-Control-Request-Method") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
