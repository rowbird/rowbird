package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/auth/oidctest"
)

func TestOIDCOverHTTP(t *testing.T) {
	idp := oidctest.New("rowbird", "client-secret")
	defer idp.Close()
	ts := newTestServerWith(t, auth.Config{BaseURL: passkeyOrigin})
	admin := ts.setup()

	var settings gen.OIDCSettings
	r := admin.do(http.MethodPut, "/api/v1/settings/oidc", map[string]any{
		"enabled": true, "issuer": idp.URL, "client_id": "rowbird", "client_secret": "client-secret", "scopes": []string{},
		"button_label": "Acme", "auto_provision": false, "default_role": "viewer", "allowed_domains": []string{},
	})
	r.decode(t, &settings)
	if r.Code != http.StatusOK || !settings.ClientSecretConfigured || !settings.Available || strings.Contains(r.Body.String(), "client-secret") {
		t.Fatalf("save: %d %s", r.Code, r.Body)
	}
	var test gen.OIDCTestResult
	admin.do(http.MethodPost, "/api/v1/settings/oidc/test", map[string]any{"issuer": idp.URL}).decode(t, &test)
	if !test.Ok {
		t.Fatalf("test %+v", test)
	}
	var status gen.SetupStatus
	ts.client().do(http.MethodGet, "/api/v1/setup/status", nil).decode(t, &status)
	if !status.OidcEnabled || status.OidcLabel == nil || *status.OidcLabel != "Acme" {
		t.Fatalf("status %+v", status)
	}

	idp.SignIn(oidctest.Identity{Subject: "sub-admin", Email: "admin@example.com", EmailVerified: true})
	c := ts.client()
	r = c.do(http.MethodGet, "/api/v1/auth/oidc/login?redirect=/reports", nil)
	flow := r.cookie(OIDCCookie)
	if r.Code != http.StatusFound || flow == nil || !flow.HttpOnly || flow.SameSite != http.SameSiteLaxMode || !strings.HasPrefix(r.Header().Get("Location"), idp.URL+"/authorize?") {
		t.Fatalf("login: %d %v %s", r.Code, flow, r.Header().Get("Location"))
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Get(r.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	back, _ := url.Parse(resp.Header.Get("Location"))
	r = c.do(http.MethodGet, back.RequestURI(), nil)
	if r.Code != http.StatusFound || r.Header().Get("Location") != "/reports" || r.cookie(SessionCookie) == nil {
		t.Fatalf("callback: %d %s", r.Code, r.Header().Get("Location"))
	}
	var me gen.Me
	c.do(http.MethodGet, "/api/v1/me", nil).decode(t, &me)
	if me.Email != "admin@example.com" {
		t.Fatalf("me %+v", me)
	}

	// A callback without the flow cookie goes back to the login page with a code, not a session.
	r = ts.client().do(http.MethodGet, back.RequestURI(), nil)
	if r.Code != http.StatusFound || r.Header().Get("Location") != "/login?oidc_error=auth.oidc_failed" || r.cookie(SessionCookie) != nil {
		t.Fatalf("forged callback: %d %s", r.Code, r.Header().Get("Location"))
	}
	// An external redirect is not followed.
	r = ts.client().do(http.MethodGet, "/api/v1/auth/oidc/login?redirect=//evil.example", nil)
	if r.Code != http.StatusFound || r.cookie(OIDCCookie) == nil {
		t.Fatalf("login with an external redirect: %d", r.Code)
	}
	if appPath(ptr("//evil.example")) != "/" || appPath(ptr("/api/v1/me")) != "/" || appPath(ptr("/runs/1")) != "/runs/1" {
		t.Fatal("appPath")
	}

	// Without a base URL the button is not offered and the flow goes back with a code.
	off := newTestServer(t)
	r = off.client().do(http.MethodGet, "/api/v1/auth/oidc/login", nil)
	if r.Header().Get("Location") != "/login?oidc_error=auth.oidc_unavailable" {
		t.Fatalf("unavailable: %s", r.Header().Get("Location"))
	}
}
