package api

import (
	"errors"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/i18n"
	"github.com/rowbird/rowbird/internal/links"
)

// Public shared link downloads are rate limited per client IP.
const (
	linkBurst  = 60
	linkWindow = time.Minute
)

// linkDownload serves GET /r/{token}: a shared link, outside the API and without authentication
// unless the link requires it (docs/spec/07-security.md, "Shared links").
func linkDownload(svc *links.Service, authSvc *auth.Service, limiter *rateLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Robots-Tag", "noindex, nofollow")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		ctx := r.Context()
		ip := ClientIPFrom(ctx)
		if ok, wait := limiter.allow("link:" + ip); !ok {
			h.Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			linkPage(w, r, http.StatusTooManyRequests, "errors."+CodeRateLimited)
			return
		}
		token := chi.URLParam(r, "token")
		var p *auth.Principal
		if c, err := r.Cookie(SessionCookie); err == nil && authSvc != nil {
			p, _ = authSvc.AuthenticateSession(ctx, c.Value)
		}
		dl, err := svc.Open(ctx, token, p, auth.RequestMeta{IP: ip, UserAgent: r.UserAgent()})
		switch {
		case errors.Is(err, links.ErrLoginRequired):
			http.Redirect(w, r, "/login?redirect="+url.QueryEscape("/r/"+token), http.StatusFound)
			return
		case errors.Is(err, links.ErrNotFound):
			linkPage(w, r, http.StatusNotFound, "errors.link.not_found")
			return
		case errors.Is(err, links.ErrGone):
			linkPage(w, r, http.StatusGone, "errors.link.expired")
			return
		case err != nil:
			linkPage(w, r, http.StatusInternalServerError, "errors.internal")
			return
		}
		if dl.RedirectURL != "" {
			http.Redirect(w, r, dl.RedirectURL, http.StatusFound)
			return
		}
		defer func() { _ = dl.Body.Close() }()
		h.Set("Content-Type", dl.ContentType)
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": dl.Name}))
		h.Set("X-Content-Type-Options", "nosniff")
		if dl.Size > 0 {
			h.Set("Content-Length", strconv.FormatInt(dl.Size, 10))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, dl.Body)
	}
}

// linkPage answers a browser with a short page in its language.
func linkPage(w http.ResponseWriter, r *http.Request, status int, key string) {
	b := i18n.Default()
	locale := b.Match(r.Header.Get("Accept-Language"))
	msg := b.T(locale, key)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `<!doctype html><html lang="`+html.EscapeString(locale)+`"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Rowbird</title></head>`+
		`<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1rem;color:#1f2328"><h1 style="font-size:1.25rem">Rowbird</h1><p>`+html.EscapeString(msg)+`</p></body></html>`)
}
