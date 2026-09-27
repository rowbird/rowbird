package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/api/gen"
)

func TestSetupOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	c := ts.client()

	var status gen.SetupStatus
	c.do(http.MethodGet, "/api/v1/setup/status", nil).decode(t, &status)
	if !status.SetupRequired || status.MasterKey.Source != gen.Generated || status.MasterKey.Path == nil {
		t.Fatalf("status %+v", status)
	}

	res := c.do(http.MethodPost, "/api/v1/setup", map[string]any{"email": "x"}, func(r *http.Request) {
		r.Header.Set("Content-Type", "text/plain")
	})
	if res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain body: %d", res.Code)
	}

	res = c.do(http.MethodPost, "/api/v1/setup", map[string]any{"email": "bad", "name": "", "password": "x", "locale": "en", "timezone": "UTC"})
	if p := res.problem(t); res.Code != 400 || p.Code != CodeValidation || p.Errors == nil || len(*p.Errors) < 3 {
		t.Fatalf("validation: %d %+v", res.Code, p)
	}

	c = ts.setup()
	session, csrf := c.cookies[SessionCookie], c.cookies[CSRFCookie]
	if session == nil || csrf == nil || !session.HttpOnly || csrf.HttpOnly || session.Path != "/" ||
		session.SameSite != http.SameSiteLaxMode || session.Secure || session.MaxAge < 6*24*3600 {
		t.Fatalf("cookies %+v %+v", session, csrf)
	}
	var after gen.SetupStatus
	c.do(http.MethodGet, "/api/v1/setup/status", nil).decode(t, &after)
	if after.SetupRequired || after.MasterKey.Path != nil {
		t.Fatalf("after setup %+v", after)
	}
	res = ts.client().do(http.MethodPost, "/api/v1/setup", map[string]any{"email": "b@example.com", "name": "B", "password": "b passphrase 1", "locale": "en", "timezone": "UTC"})
	if p := res.problem(t); res.Code != http.StatusConflict || p.Code != "setup.completed" {
		t.Fatalf("second setup: %d %+v", res.Code, p)
	}

	var me gen.Me
	c.do(http.MethodGet, "/api/v1/me", nil).decode(t, &me)
	if me.Email != "admin@example.com" || me.Role != gen.RoleAdmin || me.Restriction != gen.None {
		t.Fatalf("me %+v", me)
	}
}

func TestSecureCookies(t *testing.T) {
	ts := newTestServer(t, func(d *Deps) { d.SecureCookies = true })
	c := ts.setup()
	if !c.cookies[SessionCookie].Secure || !c.cookies[CSRFCookie].Secure {
		t.Fatal("cookies are not Secure")
	}
}

func TestCSRF(t *testing.T) {
	ts := newTestServer(t)
	c := ts.setup()
	token := c.cookies[CSRFCookie].Value

	wrong := "A" + token[1:]
	if token[0] == 'A' {
		wrong = "B" + token[1:]
	}
	for name, header := range map[string]string{"missing": "", "wrong": wrong} {
		res := c.do(http.MethodPost, "/api/v1/auth/logout", nil, func(r *http.Request) {
			r.Header.Del(CSRFHeader)
			if header != "" {
				r.Header.Set(CSRFHeader, header)
			}
		})
		if p := res.problem(t); res.Code != http.StatusForbidden || p.Code != CodeCSRFFailed {
			t.Fatalf("%s token: %d %+v", name, res.Code, p)
		}
	}
	// Safe methods do not need the token.
	if res := c.do(http.MethodGet, "/api/v1/me", nil, func(r *http.Request) { r.Header.Del(CSRFHeader) }); res.Code != http.StatusOK {
		t.Fatalf("GET without token: %d", res.Code)
	}

	res := c.do(http.MethodPost, "/api/v1/auth/logout", nil)
	if res.Code != http.StatusNoContent || res.cookie(SessionCookie).MaxAge >= 0 {
		t.Fatalf("logout: %d", res.Code)
	}
	if res := c.do(http.MethodGet, "/api/v1/me", nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", res.Code)
	}
}

func TestLoginErrorsAndLockout(t *testing.T) {
	ts := newTestServer(t)
	ts.setup()
	c := ts.client()

	unknown := c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "nobody@example.com", "password": "whatever passphrase"})
	wrong := c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.com", "password": "whatever passphrase"})
	for _, res := range []response{unknown, wrong} {
		if p := res.problem(t); res.Code != http.StatusUnauthorized || p.Code != "auth.invalid_credentials" {
			t.Fatalf("login failure: %d %+v", res.Code, p)
		}
	}
	if unknown.problem(t).Title != wrong.problem(t).Title {
		t.Fatal("unknown account and wrong password answer differently")
	}
	for range 4 {
		c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.com", "password": "whatever passphrase"})
	}
	res := c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.com", "password": testAdminPassword})
	if p := res.problem(t); res.Code != http.StatusTooManyRequests || p.Code != "auth.locked" || res.Header().Get("Retry-After") == "" {
		t.Fatalf("locked: %d %+v %q", res.Code, p, res.Header().Get("Retry-After"))
	}
}

func TestLoginRateLimitPerIP(t *testing.T) {
	ts := newTestServer(t)
	ts.setup()
	attacker := ts.client()
	var last response
	for range loginBurst + 1 {
		last = attacker.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "x@example.com", "password": "whatever passphrase"})
	}
	if p := last.problem(t); last.Code != http.StatusTooManyRequests || p.Code != CodeRateLimited || last.Header().Get("Retry-After") == "" {
		t.Fatalf("after %d attempts: %d %+v", loginBurst+1, last.Code, p)
	}
	// Another client is not affected, also when it comes through a trusted proxy.
	other := ts.client()
	other.remote = "192.0.2.10:443"
	other.headers["X-Forwarded-For"] = "203.0.113.99"
	res := other.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.com", "password": testAdminPassword})
	if res.Code != http.StatusOK {
		t.Fatalf("other client: %d %s", res.Code, res.Body)
	}
}

func TestTwoFactorLoginOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.setup()
	var setup gen.TotpSetup
	admin.do(http.MethodPost, "/api/v1/me/2fa/totp/setup", nil).decode(t, &setup)
	if !strings.HasPrefix(setup.QrCode, "data:image/png;base64,") {
		t.Fatalf("setup %+v", setup.OtpauthUrl)
	}
	code := totpCode(t, setup.Secret, time.Now())
	var codes gen.RecoveryCodes
	res := admin.do(http.MethodPost, "/api/v1/me/2fa/totp/confirm", map[string]any{"code": code})
	res.decode(t, &codes)
	if res.Code != http.StatusOK || len(codes.Codes) != 10 {
		t.Fatalf("confirm: %d %s", res.Code, res.Body)
	}

	c := ts.client()
	var login gen.LoginResponse
	res = c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.com", "password": testAdminPassword})
	res.decode(t, &login)
	if login.Status != gen.MfaRequired || login.ChallengeToken == nil || res.cookie(SessionCookie) != nil {
		t.Fatalf("login: %+v", login)
	}
	res = c.do(http.MethodPost, "/api/v1/auth/login/second-factor", map[string]any{"challenge_token": *login.ChallengeToken, "code": codes.Codes[0]})
	if res.Code != http.StatusOK || c.cookies[SessionCookie] == nil {
		t.Fatalf("second factor: %d %s", res.Code, res.Body)
	}
}

func TestRestrictedSessionAndAPIKeys(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.setup()
	_, temp := admin.createUser("viewer@example.com", "viewer")

	viewer := ts.signIn("viewer@example.com", temp)
	var me gen.Me
	viewer.do(http.MethodGet, "/api/v1/me", nil).decode(t, &me)
	if me.Restriction != gen.PasswordChangeRequired {
		t.Fatalf("restriction %q", me.Restriction)
	}
	res := viewer.do(http.MethodGet, "/api/v1/users", nil)
	if p := res.problem(t); res.Code != http.StatusForbidden || p.Code != "auth.password_change_required" {
		t.Fatalf("restricted list: %d %+v", res.Code, p)
	}
	viewer = ts.activate("viewer@example.com", temp)
	if res := viewer.do(http.MethodGet, "/api/v1/users", nil); res.Code != http.StatusOK {
		t.Fatalf("after change: %d", res.Code)
	}
	if res := viewer.do(http.MethodPost, "/api/v1/users", map[string]any{"email": "z@example.com", "name": "Z", "role": "viewer"}); res.Code != http.StatusForbidden {
		t.Fatalf("viewer creates user: %d", res.Code)
	}

	key := admin.createKey("read")
	bot := ts.client()
	bot.bearer = key
	if res := bot.do(http.MethodGet, "/api/v1/users", nil); res.Code != http.StatusOK {
		t.Fatalf("read key lists users: %d %s", res.Code, res.Body)
	}
	if res := bot.do(http.MethodPost, "/api/v1/users", map[string]any{"email": "z@example.com", "name": "Z", "role": "viewer"}); res.Code != http.StatusForbidden {
		t.Fatalf("read key creates user: %d", res.Code)
	}
	if res := bot.do(http.MethodGet, "/api/v1/me/sessions", nil); res.Code != http.StatusForbidden {
		t.Fatalf("key on session-only endpoint: %d", res.Code)
	}
	// POST with a key needs no CSRF token.
	if res := bot.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "a", "password": "b"}); res.Code != http.StatusUnauthorized {
		t.Fatalf("login with key header: %d", res.Code)
	}

	both := ts.client()
	both.bearer = key
	both.cookies = admin.cookies
	if res := both.do(http.MethodGet, "/api/v1/me", nil); res.Code != http.StatusBadRequest {
		t.Fatalf("cookie and bearer together: %d", res.Code)
	}
	for _, bad := range []string{"rbk_nope", "Basic abc"} {
		c := ts.client()
		c.headers["Authorization"] = bad
		if res := c.do(http.MethodGet, "/api/v1/me", nil); res.Code != http.StatusUnauthorized {
			t.Fatalf("%q: %d", bad, res.Code)
		}
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	cases := []struct {
		remote, xff, want string
	}{
		{"203.0.113.5:1234", "", "203.0.113.5"},
		{"203.0.113.5:1234", "1.2.3.4", "203.0.113.5"},                       // untrusted peer: header ignored
		{"10.0.0.2:1234", "198.51.100.7", "198.51.100.7"},                    // trusted proxy
		{"10.0.0.2:1234", "6.6.6.6, 198.51.100.7, 10.0.0.9", "198.51.100.7"}, // skip trusted hops, ignore spoofed left part
		{"[::1]:80", "2001:db8::1", "2001:db8::1"},
		{"10.0.0.2:1234", "not-an-ip", "10.0.0.2"},
	}
	for _, tc := range cases {
		var got string
		h := clientIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = ClientIPFrom(r.Context()) }))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = tc.remote
		if tc.xff != "" {
			req.Header.Set("X-Forwarded-For", tc.xff)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if got != tc.want {
			t.Errorf("remote %s xff %q: got %s, want %s", tc.remote, tc.xff, got, tc.want)
		}
	}
}

func TestRateLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(3, 30*time.Second, func() time.Time { return now })
	for range 3 {
		if ok, _ := l.allow("a"); !ok {
			t.Fatal("burst denied")
		}
	}
	ok, wait := l.allow("a")
	if ok || wait <= 0 || wait > 10*time.Second {
		t.Fatalf("fourth: ok=%v wait=%s", ok, wait)
	}
	if ok, _ := l.allow("b"); !ok {
		t.Fatal("other key limited")
	}
	now = now.Add(10 * time.Second)
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("token not refilled")
	}
	now = now.Add(time.Hour)
	l.allow("c")
	if _, kept := l.buckets["a"]; kept {
		t.Fatal("idle bucket not collected")
	}
}
