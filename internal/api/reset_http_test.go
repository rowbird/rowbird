package api

import (
	"net/http"
	"testing"
)

func TestPasswordResetOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	ts.setup()
	c := ts.client()
	for _, email := range []string{"admin@example.com", "nobody@example.com"} {
		if r := c.do(http.MethodPost, "/api/v1/auth/password-reset", map[string]any{"email": email}); r.Code != http.StatusAccepted || r.Body.Len() != 0 {
			t.Fatalf("%s: %d %s", email, r.Code, r.Body)
		}
	}
	r := c.do(http.MethodPost, "/api/v1/auth/password-reset/confirm", map[string]any{"token": "rbr_nope", "password": "a brand new passphrase"})
	if p := r.problem(t); r.Code != http.StatusGone || p.Code != "auth.reset_invalid" {
		t.Fatalf("%d %+v", r.Code, p)
	}
}
