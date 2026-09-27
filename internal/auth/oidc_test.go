package auth_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/auth/oidctest"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

func oidcEnv(t *testing.T, s *store.Store, idp *oidctest.Provider, cfg auth.OIDCConfig) *env {
	t.Helper()
	e := newEnv(t, s, auth.Config{BaseURL: origin}).withAdmin()
	cfg.Enabled, cfg.Issuer, cfg.ClientID = true, idp.URL, idp.ClientID
	secret := idp.ClientSecret
	got, err := e.svc.UpdateOIDCSettings(e.wsCtx, e.admin.Principal, auth.OIDCInput{OIDCConfig: cfg, ClientSecret: &secret}, e.meta)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SecretConfigured || got.RedirectURI != origin+auth.CallbackPath || strings.Join(got.Scopes, " ") != "openid email profile" {
		t.Fatalf("settings %+v", got)
	}
	return e
}

// signInWithOIDC runs the browser's part: leave for the provider, come back with the code.
func signInWithOIDC(t *testing.T, e *env) (*auth.IssuedSession, string, error) {
	t.Helper()
	return signInWithOIDCState(t, e, "")
}

// signInWithOIDCState is signInWithOIDC, coming back with forceState when it is not empty.
func signInWithOIDCState(t *testing.T, e *env, forceState string) (*auth.IssuedSession, string, error) {
	t.Helper()
	target, cookie, err := e.svc.BeginOIDC(t.Context(), "/reports")
	if err != nil {
		return nil, "", err
	}
	u, _ := url.Parse(target)
	if u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("nonce") == "" {
		t.Fatalf("authorization URL %s", target)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	back, _ := url.Parse(resp.Header.Get("Location"))
	state := back.Query().Get("state")
	if forceState != "" {
		state = forceState
	}
	return e.svc.FinishOIDC(t.Context(), cookie, state, back.Query().Get("code"), e.meta)
}

func TestOIDC(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		idp := oidctest.New("rowbird", "client-secret")
		defer idp.Close()
		e := oidcEnv(t, s, idp, auth.OIDCConfig{ButtonLabel: "Acme SSO"})
		if on, label := e.svc.OIDCPublic(t.Context()); !on || label != "Acme SSO" {
			t.Fatalf("public %v %q", on, label)
		}

		// A workspace that requires 2FA does not ask OIDC sessions for Rowbird's own.
		yes := true
		if _, err := e.svc.UpdateSettings(e.wsCtx, e.admin.Principal, auth.SettingsPatch{Require2FA: &yes}, e.meta); err != nil {
			t.Fatal(err)
		}

		// An unverified email never links to an existing account.
		idp.SignIn(oidctest.Identity{Subject: "sub-ana", Email: "Admin@Example.com", EmailVerified: false})
		if _, _, err := signInWithOIDC(t, e); !errors.Is(err, auth.ErrOIDCUnverified) {
			t.Fatalf("unverified: %v", err)
		}
		idp.SignIn(oidctest.Identity{Subject: "sub-ana", Email: "Admin@Example.com", EmailVerified: true})
		sess, target, err := signInWithOIDC(t, e)
		if err != nil || target != "/reports" || sess.Principal.UserID != e.admin.Principal.UserID || sess.Principal.Restriction != auth.RestrictionNone {
			t.Fatalf("linked login: %+v %q %v", sess, target, err)
		}
		if len(events(t, e, auth.EventOIDCLinked)) != 1 {
			t.Fatal("no oidc_linked event")
		}
		// Later sign-ins find the identity, whatever the email says now.
		idp.SignIn(oidctest.Identity{Subject: "sub-ana", Email: "someone-else@example.com", EmailVerified: true})
		if sess, _, err := signInWithOIDC(t, e); err != nil || sess.Principal.UserID != e.admin.Principal.UserID {
			t.Fatalf("by subject: %v", err)
		}

		// Nobody else gets in without provisioning.
		idp.SignIn(oidctest.Identity{Subject: "sub-bia", Email: "bia@example.com", EmailVerified: true, Name: "Bia"})
		if _, _, err := signInWithOIDC(t, e); !errors.Is(err, auth.ErrOIDCNoAccount) {
			t.Fatalf("no account: %v", err)
		}

		// A nonce that does not match, a state that does not match: refused.
		idp.WrongNonce(true)
		idp.SignIn(oidctest.Identity{Subject: "sub-ana", Email: "admin@example.com", EmailVerified: true})
		if _, _, err := signInWithOIDC(t, e); !errors.Is(err, auth.ErrOIDCFailed) {
			t.Fatalf("wrong nonce: %v", err)
		}
		idp.WrongNonce(false)
		if _, _, err := signInWithOIDCState(t, e, "forged-state"); !errors.Is(err, auth.ErrOIDCFailed) {
			t.Fatalf("forged state: %v", err)
		}
		if _, _, err := e.svc.FinishOIDC(t.Context(), "v1:not:a:cookie", "s", "c", e.meta); !errors.Is(err, auth.ErrOIDCFailed) {
			t.Fatalf("forged cookie: %v", err)
		}
	})
}

func TestOIDCProvisioning(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		idp := oidctest.New("rowbird", "client-secret")
		defer idp.Close()
		e := oidcEnv(t, s, idp, auth.OIDCConfig{AutoProvision: true, DefaultRole: store.RoleEditor, AllowedDomains: []string{"Example.com", "@acme.io"}})

		idp.SignIn(oidctest.Identity{Subject: "sub-bia", Email: "bia@acme.io", EmailVerified: true, Name: "Bia"})
		sess, _, err := signInWithOIDC(t, e)
		if err != nil || sess.Principal.Role != store.RoleEditor {
			t.Fatalf("provisioned: %+v %v", sess, err)
		}
		u, _ := s.Users().Get(t.Context(), sess.Principal.UserID)
		if u.Name != "Bia" || u.PasswordHash != nil || u.OIDCSubject == nil || *u.OIDCSubject != "sub-bia" {
			t.Fatalf("user %+v", u)
		}
		if len(events(t, e, auth.EventUserProvisioned)) != 1 {
			t.Fatal("no user_provisioned event")
		}
		// Password confirmations are skipped for accounts without a password.
		if _, err := e.svc.BeginPasskeyRegistration(t.Context(), sess.Principal, ""); err != nil {
			t.Fatalf("passkey for an OIDC account: %v", err)
		}

		idp.SignIn(oidctest.Identity{Subject: "sub-eve", Email: "eve@evil.example", EmailVerified: true})
		if _, _, err := signInWithOIDC(t, e); !errors.Is(err, auth.ErrOIDCDomain) {
			t.Fatalf("domain: %v", err)
		}
		idp.SignIn(oidctest.Identity{Subject: "sub-cai", Email: "cai@acme.io", EmailVerified: false})
		if _, _, err := signInWithOIDC(t, e); !errors.Is(err, auth.ErrOIDCUnverified) {
			t.Fatalf("unverified with an allowlist: %v", err)
		}

		// A disabled user stays out.
		fresh, _ := e.svc.GetUser(e.wsCtx, u.ID)
		if _, err := e.svc.UpdateUser(e.wsCtx, e.admin.Principal, u.ID, auth.UserPatch{Version: fresh.User.Version, Disabled: ptr(true)}, e.meta); err != nil {
			t.Fatal(err)
		}
		idp.SignIn(oidctest.Identity{Subject: "sub-bia", Email: "bia@acme.io", EmailVerified: true})
		if _, _, err := signInWithOIDC(t, e); !errors.Is(err, auth.ErrOIDCNoAccount) {
			t.Fatalf("disabled: %v", err)
		}
	})
}

func TestOIDCSettingsValidation(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{}).withAdmin()
		_, err := e.svc.UpdateOIDCSettings(e.wsCtx, e.admin.Principal, auth.OIDCInput{OIDCConfig: auth.OIDCConfig{
			Enabled: true, Issuer: "ftp://idp", DefaultRole: store.RoleAdmin, AllowedDomains: []string{"not a domain"},
		}}, e.meta)
		wantField(t, err, "issuer", "validation.url")
		wantField(t, err, "client_id", auth.CodeRequired)
		wantField(t, err, "default_role", auth.CodeInvalidValue)
		wantField(t, err, "allowed_domains", auth.CodeInvalidValue)
		// Without a base URL there is no redirect URI, so no OIDC.
		if _, _, err := e.svc.BeginOIDC(t.Context(), "/"); !errors.Is(err, auth.ErrOIDCUnavailable) {
			t.Fatalf("no base URL: %v", err)
		}
		if err := e.svc.TestOIDC(t.Context(), "http://127.0.0.1:1"); !errors.Is(err, auth.ErrOIDCDiscovery) {
			t.Fatalf("discovery: %v", err)
		}
	})
}
