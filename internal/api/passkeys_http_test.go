package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/auth/webauthntest"
)

const passkeyOrigin = "https://rowbird.example.com"

func TestPasskeysOverHTTP(t *testing.T) {
	ts := newTestServerWith(t, auth.Config{BaseURL: passkeyOrigin})
	admin := ts.setup()
	a := webauthntest.New(passkeyOrigin)

	var status gen.SetupStatus
	ts.client().do(http.MethodGet, "/api/v1/setup/status", nil).decode(t, &status)
	if !status.PasskeysAvailable {
		t.Fatal("passkeys not available")
	}

	var ceremony gen.PasskeyCeremony
	r := admin.do(http.MethodPost, "/api/v1/me/passkeys/options", map[string]any{"password": testAdminPassword})
	r.decode(t, &ceremony)
	if r.Code != http.StatusOK || ceremony.Options["challenge"] == nil {
		t.Fatalf("options: %d %s", r.Code, r.Body)
	}
	opts, _ := json.Marshal(ceremony.Options)
	answer, _, err := a.Register(opts)
	if err != nil {
		t.Fatal(err)
	}
	var credential map[string]any
	_ = json.Unmarshal(answer, &credential)
	var pk gen.Passkey
	r = admin.do(http.MethodPost, "/api/v1/me/passkeys", map[string]any{"challenge_token": ceremony.ChallengeToken, "name": "Laptop", "credential": credential})
	r.decode(t, &pk)
	if r.Code != http.StatusCreated || pk.Name != "Laptop" || !pk.Synced {
		t.Fatalf("add: %d %s", r.Code, r.Body)
	}

	// Passwordless sign-in from a fresh browser sets the session cookies.
	c := ts.client()
	r = c.do(http.MethodPost, "/api/v1/auth/passkey/options", nil)
	r.decode(t, &ceremony)
	opts, _ = json.Marshal(ceremony.Options)
	answer, err = a.Assert(opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(answer, &credential)
	var me gen.Me
	r = c.do(http.MethodPost, "/api/v1/auth/passkey", map[string]any{"challenge_token": ceremony.ChallengeToken, "credential": credential})
	r.decode(t, &me)
	if r.Code != http.StatusOK || r.cookie(SessionCookie) == nil || me.Email != "admin@example.com" {
		t.Fatalf("passkey login: %d %s", r.Code, r.Body)
	}

	// A password login offers the passkey as the second factor.
	var login gen.LoginResponse
	c = ts.client()
	c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.com", "password": testAdminPassword}).decode(t, &login)
	if login.Status != gen.MfaRequired || login.Methods == nil || (*login.Methods)[0] != gen.LoginResponseMethodsPasskey {
		t.Fatalf("login %+v", login)
	}
	var po gen.PasskeyOptions
	c.do(http.MethodPost, "/api/v1/auth/login/second-factor/passkey", map[string]any{"challenge_token": *login.ChallengeToken}).decode(t, &po)
	opts, _ = json.Marshal(po.Options)
	answer, _ = a.Assert(opts, nil)
	_ = json.Unmarshal(answer, &credential)
	r = c.do(http.MethodPost, "/api/v1/auth/login/second-factor", map[string]any{"challenge_token": *login.ChallengeToken, "passkey": credential})
	if r.Code != http.StatusOK || r.cookie(SessionCookie) == nil {
		t.Fatalf("second factor: %d %s", r.Code, r.Body)
	}

	var list gen.PasskeyList
	admin.do(http.MethodGet, "/api/v1/me/passkeys", nil).decode(t, &list)
	if len(list.Items) != 1 || list.Items[0].LastUsedAt == nil {
		t.Fatalf("list %+v", list)
	}
	if r := admin.do(http.MethodPost, "/api/v1/me/passkeys/"+pk.Id.String()+"/remove", map[string]any{"password": "nope nope nope"}); r.Code != http.StatusBadRequest {
		t.Fatalf("remove with a wrong password: %d", r.Code)
	}
	if r := admin.do(http.MethodPost, "/api/v1/me/passkeys/"+pk.Id.String()+"/remove", map[string]any{"password": testAdminPassword}); r.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", r.Code, r.Body)
	}

	// Without a base URL the endpoints say why.
	off := newTestServer(t)
	r = off.client().do(http.MethodPost, "/api/v1/auth/passkey/options", nil)
	if p := r.problem(t); r.Code != http.StatusConflict || p.Code != "passkey.unavailable" {
		t.Fatalf("unavailable: %d %+v", r.Code, p)
	}
}
