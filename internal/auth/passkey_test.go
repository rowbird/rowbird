package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/auth/webauthntest"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

const origin = "https://rowbird.example.com"

// addPasskey registers a passkey for the session's user.
func addPasskey(t *testing.T, e *env, p *auth.Principal, a *webauthntest.Authenticator, password, name string) (*store.Passkey, *webauthntest.Credential) {
	t.Helper()
	c, err := e.svc.BeginPasskeyRegistration(e.t.Context(), p, password)
	if err != nil {
		t.Fatal(err)
	}
	answer, cred, err := a.Register(c.Options)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := e.svc.FinishPasskeyRegistration(e.t.Context(), p, c.Token, name, answer, e.meta)
	if err != nil {
		t.Fatal(err)
	}
	return pk, cred
}

func passkeyLogin(t *testing.T, e *env, a *webauthntest.Authenticator, use *webauthntest.Credential) (*auth.IssuedSession, error) {
	t.Helper()
	c, err := e.svc.BeginPasskeyLogin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	answer, err := a.Assert(c.Options, use)
	if err != nil {
		t.Fatal(err)
	}
	return e.svc.FinishPasskeyLogin(t.Context(), c.Token, answer, e.meta)
}

func TestPasskeys(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{BaseURL: origin}).withAdmin()
		if !e.svc.PasskeysAvailable() {
			t.Fatal("passkeys unavailable with a base URL")
		}
		a := webauthntest.New(origin)
		p := e.admin.Principal

		if _, err := e.svc.BeginPasskeyRegistration(t.Context(), p, "wrong password"); err == nil {
			t.Fatal("registration without the password")
		}
		pk, cred := addPasskey(t, e, p, a, adminPassword, " Laptop ")
		if pk.Name != "Laptop" || !auth.PasskeySynced(pk) {
			t.Fatalf("passkey %+v", pk)
		}
		if len(events(t, e, auth.EventPasskeyAdded)) != 1 {
			t.Fatal("no passkey_added event")
		}

		// A password login now asks for the second factor, which the passkey answers.
		res, err := e.svc.Login(t.Context(), "admin@example.com", adminPassword, e.meta)
		if err != nil || res.Session != nil || strings.Join(res.Methods, ",") != "passkey" {
			t.Fatalf("login: %+v %v", res, err)
		}
		opts, err := e.svc.BeginPasskeySecondFactor(t.Context(), res.ChallengeToken)
		if err != nil {
			t.Fatal(err)
		}
		answer, err := a.Assert(opts, nil)
		if err != nil {
			t.Fatal(err)
		}
		sess, err := e.svc.LoginSecondFactor(t.Context(), res.ChallengeToken, auth.SecondFactor{Passkey: answer}, e.meta)
		if err != nil || sess.Principal.UserID != p.UserID {
			t.Fatalf("second factor: %v", err)
		}
		// The ceremony is single use.
		if _, err := e.svc.LoginSecondFactor(t.Context(), res.ChallengeToken, auth.SecondFactor{Passkey: answer}, e.meta); err == nil {
			t.Fatal("replayed passkey answer accepted")
		}

		// Passwordless sign-in.
		sess, err = passkeyLogin(t, e, a, nil)
		if err != nil || sess.Principal.UserID != p.UserID || sess.Principal.Restriction != auth.RestrictionNone {
			t.Fatalf("passkey login: %+v %v", sess, err)
		}
		keys, _ := e.svc.ListPasskeys(t.Context(), p)
		if len(keys) != 1 || keys[0].LastUsedAt == nil || keys[0].SignCount != int64(cred.SignCount) {
			t.Fatalf("after use %+v", keys)
		}

		// A signature counter that goes back means a cloned key: refused, and counted as a failure.
		cred.SignCount = 0
		if _, err := passkeyLogin(t, e, a, cred); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("cloned key: %v", err)
		}

		// Requiring 2FA does not restrict a user whose second factor is a passkey.
		yes := true
		if _, err := e.svc.UpdateSettings(e.wsCtx, p, auth.SettingsPatch{Require2FA: &yes}, e.meta); err != nil {
			t.Fatal(err)
		}
		cred.SignCount = 100
		if sess, err = passkeyLogin(t, e, a, cred); err != nil || sess.Principal.Restriction != auth.RestrictionNone {
			t.Fatalf("with 2FA required: %+v %v", sess, err)
		}

		renamed, err := e.svc.RenamePasskey(t.Context(), p, pk.ID, "Phone")
		if err != nil || renamed.Name != "Phone" {
			t.Fatalf("rename: %v", err)
		}
		if err := e.svc.RemovePasskey(t.Context(), p, pk.ID, "wrong password", e.meta); err == nil {
			t.Fatal("removed without the password")
		}
		if err := e.svc.RemovePasskey(t.Context(), p, pk.ID, adminPassword, e.meta); err != nil {
			t.Fatal(err)
		}
		if _, err := passkeyLogin(t, e, a, cred); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("removed passkey still signs in: %v", err)
		}
	})
}

func TestPasskeyFailures(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{BaseURL: origin}).withAdmin()
		p := e.admin.Principal

		// An answer made for another site is refused.
		c, _ := e.svc.BeginPasskeyRegistration(t.Context(), p, adminPassword)
		answer, _, _ := webauthntest.New("https://evil.example.com").Register(c.Options)
		if _, err := e.svc.FinishPasskeyRegistration(t.Context(), p, c.Token, "x", answer, e.meta); !errors.Is(err, auth.ErrPasskeyFailed) {
			t.Fatalf("wrong origin: %v", err)
		}

		// Passwordless sign-in demands user verification (PIN or biometrics).
		a := webauthntest.New(origin)
		_, cred := addPasskey(t, e, p, a, adminPassword, "key")
		a.SkipUV = true
		if _, err := passkeyLogin(t, e, a, cred); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("without user verification: %v", err)
		}

		// A disabled user cannot sign in with a passkey.
		u, temp := e.createUser("ed@example.com", store.RoleEditor)
		edSess := e.login("ed@example.com", temp)
		if err := e.svc.ChangePassword(t.Context(), edSess.Principal, temp, "editor passphrase 1", e.meta); err != nil {
			t.Fatal(err)
		}
		edKey := webauthntest.New(origin)
		addPasskey(t, e, edSess.Principal, edKey, "editor passphrase 1", "ed")
		fresh, _ := e.svc.GetUser(e.wsCtx, u.User.ID)
		if _, err := e.svc.UpdateUser(e.wsCtx, p, u.User.ID, auth.UserPatch{Version: fresh.User.Version, Disabled: ptr(true)}, e.meta); err != nil {
			t.Fatal(err)
		}
		if _, err := passkeyLogin(t, e, edKey, nil); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("disabled user: %v", err)
		}

		// An admin reset of 2FA removes passkeys too.
		if err := e.svc.AdminDisableTOTP(e.wsCtx, p, u.User.ID, e.meta); err != nil {
			t.Fatal(err)
		}
		if n, _ := s.Passkeys().CountByUser(t.Context(), u.User.ID); n != 0 {
			t.Fatalf("%d passkeys left after the reset", n)
		}

		// Without a base URL there are no passkeys.
		off := newEnv(t, s, auth.Config{})
		if _, err := off.svc.BeginPasskeyLogin(t.Context()); !errors.Is(err, auth.ErrPasskeysUnavailable) {
			t.Fatalf("no base URL: %v", err)
		}
		// Nothing about the keys reaches the logs.
		if strings.Contains(e.logs.String(), string(answer)) {
			t.Fatal("a WebAuthn answer was logged")
		}
	})
}
