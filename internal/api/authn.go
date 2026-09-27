package api

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/rowbird/rowbird/internal/auth"
)

// Cookie names (docs/spec/05-api.md, "Authentication").
const (
	SessionCookie = "rowbird_session"
	CSRFCookie    = "rowbird_csrf"
	CSRFHeader    = "X-CSRF-Token"
)

type principalKey struct{}

// PrincipalFrom returns the authenticated caller, or nil.
func PrincipalFrom(ctx context.Context) *auth.Principal {
	p, _ := ctx.Value(principalKey{}).(*auth.Principal)
	return p
}

type requestMetaKey struct{}

func requestMetaFrom(ctx context.Context) auth.RequestMeta {
	m, _ := ctx.Value(requestMetaKey{}).(auth.RequestMeta)
	return m
}

// authenticate resolves the caller from the session cookie or a bearer API key. It never rejects a
// request by itself: public operations must keep working with a stale cookie. Authorization
// decides what an anonymous caller may do. Sending both credentials is always an error.
func authenticate(svc *auth.Service, problems *problemWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ctx = context.WithValue(ctx, requestMetaKey{}, auth.RequestMeta{IP: ClientIPFrom(ctx), UserAgent: r.UserAgent()})

			bearer, hasBearer := bearerToken(r)
			cookie, _ := r.Cookie(SessionCookie)
			var (
				p   *auth.Principal
				err error
			)
			switch {
			case hasBearer && cookie != nil:
				problems.write(w, r, &Error{Status: http.StatusBadRequest, Code: CodeRequestInvalid, Detail: "send either a session cookie or an API key, not both"})
				return
			case hasBearer:
				p, err = svc.AuthenticateAPIKey(ctx, bearer)
			case cookie != nil:
				p, err = svc.AuthenticateSession(ctx, cookie.Value)
			}
			if err != nil && !errors.Is(err, auth.ErrUnauthenticated) {
				problems.writeError(w, r, err)
				return
			}
			if p != nil {
				ctx = context.WithValue(ctx, principalKey{}, p)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken extracts "Authorization: Bearer <token>". A malformed header still counts as a
// bearer attempt, so it fails authentication instead of being ignored.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	scheme, token, _ := strings.Cut(h, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", true
	}
	return strings.TrimSpace(token), true
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// csrfProtect guards state-changing API requests. Session-authenticated requests must echo the CSRF
// token; any request with a body must be JSON, which a cross-site HTML form cannot send without a
// CORS preflight (and Rowbird allows no cross-origin requests). Bearer requests are not exposed to
// CSRF because browsers never attach the header on their own.
func csrfProtect(svc *auth.Service, problems *problemWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) || !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}
			if hasBody(r) {
				mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || mt != "application/json" {
					problems.write(w, r, &Error{Status: http.StatusUnsupportedMediaType, Code: CodeUnsupportedMedia})
					return
				}
			}
			if p := PrincipalFrom(r.Context()); p != nil && !p.IsAPIKey() && !svc.VerifyCSRF(p, r.Header.Get(CSRFHeader)) {
				problems.write(w, r, &Error{Status: http.StatusForbidden, Code: CodeCSRFFailed})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func hasBody(r *http.Request) bool {
	return r.ContentLength > 0 || (r.ContentLength < 0 && r.Body != nil && r.Body != http.NoBody) || len(r.TransferEncoding) > 0
}

// cookieWriter issues and clears the session cookies.
type cookieWriter struct{ secure bool }

func (c cookieWriter) set(w http.ResponseWriter, s *auth.IssuedSession) {
	maxAge := int(time.Until(s.ExpiresAt).Seconds())
	// Secure follows the public base URL: plain-HTTP development setups would otherwise lose the
	// cookie. The config warns when the base URL is http.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is set from configuration, see above
		Name: SessionCookie, Value: s.Token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: c.secure, SameSite: http.SameSiteLaxMode,
	})
	// Readable by the SPA so it can echo the value in X-CSRF-Token; HttpOnly would defeat that.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // intentionally not HttpOnly
		Name: CSRFCookie, Value: s.CSRFToken, Path: "/", MaxAge: maxAge,
		HttpOnly: false, Secure: c.secure, SameSite: http.SameSiteLaxMode,
	})
}

func (c cookieWriter) clear(w http.ResponseWriter) {
	for _, name := range []string{SessionCookie, CSRFCookie} {
		http.SetCookie(w, &http.Cookie{ //nolint:gosec // expiring cookie with the same attributes as when set
			Name: name, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: name == SessionCookie, Secure: c.secure, SameSite: http.SameSiteLaxMode,
		})
	}
}
