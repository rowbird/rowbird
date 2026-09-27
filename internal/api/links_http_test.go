package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestPublicLinkPage(t *testing.T) {
	ts := newTestServer(t)
	cl := ts.client()
	res := cl.do(http.MethodGet, "/r/rbl_unknown", nil, func(r *http.Request) { r.Header.Set("Accept-Language", "pt-BR") })
	h := res.Result().Header
	if res.Code != http.StatusNotFound || h.Get("X-Robots-Tag") != "noindex, nofollow" || h.Get("Referrer-Policy") != "no-referrer" ||
		h.Get("Cache-Control") != "no-store" || !strings.Contains(res.Body.String(), "Este link não existe") {
		t.Fatalf("page %d %v %s", res.Code, h, res.Body)
	}
	// The public route is not an API route and needs no session or CSRF token.
	if res := cl.do(http.MethodGet, "/r/whatever", nil); res.Code != http.StatusNotFound || strings.Contains(res.Header().Get("Content-Type"), "problem") {
		t.Fatalf("plain token: %d %s", res.Code, res.Header())
	}
	for range linkBurst {
		cl.do(http.MethodGet, "/r/rbl_x", nil)
	}
	if res := cl.do(http.MethodGet, "/r/rbl_x", nil); res.Code != http.StatusTooManyRequests || res.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit: %d", res.Code)
	}
}
