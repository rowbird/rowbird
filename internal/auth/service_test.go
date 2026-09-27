package auth_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/logging"
	"github.com/rowbird/rowbird/internal/security"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

const adminPassword = "admin passphrase 1"

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	t     *testing.T
	store *store.Store
	svc   *auth.Service
	clock *fakeClock
	logs  *bytes.Buffer
	meta  auth.RequestMeta
	admin *auth.IssuedSession
	wsCtx context.Context
}

func newEnv(t *testing.T, s *store.Store, cfg auth.Config) *env {
	t.Helper()
	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	kr, err := crypto.NewKeyring(key)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger, _ := logging.New(&logs, "debug", "json")
	clock := &fakeClock{t: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	svc := auth.NewService(s, kr, cfg, auth.Options{
		Hasher: auth.NewPasswordHasher(auth.Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, 4),
		Now:    clock.Now,
		Logger: logger,
	})
	return &env{t: t, store: s, svc: svc, clock: clock, logs: &logs, meta: auth.RequestMeta{IP: "203.0.113.7", UserAgent: "test"}}
}

// withAdmin runs setup and keeps the admin session.
func (e *env) withAdmin() *env {
	e.t.Helper()
	sess, err := e.svc.Setup(e.t.Context(), auth.SetupInput{
		Email: "admin@example.com", Name: "Admin", Password: adminPassword, Locale: "en", Timezone: "America/Sao_Paulo",
	}, e.meta)
	if err != nil {
		e.t.Fatal(err)
	}
	e.admin = sess
	e.wsCtx = sess.Principal.Context(e.t.Context())
	return e
}

func (e *env) createUser(email string, role store.Role) (*auth.UserWithRole, string) {
	e.t.Helper()
	u, pw, err := e.svc.CreateUser(e.wsCtx, e.admin.Principal, auth.NewUserInput{Email: email, Name: "Someone", Role: role}, e.meta)
	if err != nil {
		e.t.Fatal(err)
	}
	return u, pw
}

func (e *env) login(email, password string) *auth.IssuedSession {
	e.t.Helper()
	res, err := e.svc.Login(e.t.Context(), email, password, e.meta)
	if err != nil {
		e.t.Fatalf("login %s: %v", email, err)
	}
	if res.Session == nil {
		e.t.Fatalf("login %s asked for a second factor", email)
	}
	return res.Session
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func wantField(t *testing.T, err error, field, code string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Kind != apperr.KindInvalid {
		t.Fatalf("got %v, want a validation error", err)
	}
	for _, f := range e.Fields {
		if f.Field == field && f.Code == code {
			return
		}
	}
	t.Fatalf("fields %+v lack %s=%s", e.Fields, field, code)
}

func events(t *testing.T, e *env, typ string) []store.SecurityEvent {
	t.Helper()
	page, err := e.svc.ListSecurityEvents(e.wsCtx, store.SecurityEventFilter{Type: typ}, store.PageRequest{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

func TestSetup(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{})
		ctx := t.Context()
		st, err := e.svc.SetupStatus(ctx)
		if err != nil || !st.Required || st.TokenRequired {
			t.Fatalf("status %+v %v", st, err)
		}

		_, err = e.svc.Setup(ctx, auth.SetupInput{Email: "nope", Name: "", Password: "short", Locale: "fr", Timezone: "Mars/Olympus"}, e.meta)
		for field, code := range map[string]string{
			"email": "validation.email", "name": "validation.required", "password": "validation.too_short",
			"locale": "validation.invalid_value", "timezone": "validation.invalid_value",
		} {
			wantField(t, err, field, code)
		}

		e.withAdmin()
		p := e.admin.Principal
		if p.Role != store.RoleAdmin || p.Restriction != auth.RestrictionNone || e.admin.Token == "" || e.admin.CSRFToken == "" {
			t.Fatalf("setup session %+v", p)
		}
		if st, _ := e.svc.SetupStatus(ctx); st.Required {
			t.Fatal("setup still required")
		}
		_, err = e.svc.Setup(ctx, auth.SetupInput{Email: "x@example.com", Name: "X", Password: "another passphrase", Locale: "en", Timezone: "UTC"}, e.meta)
		wantCode(t, err, "setup.completed")

		settings, err := e.svc.GetSettings(e.wsCtx)
		if err != nil || settings.DefaultTimezone != "America/Sao_Paulo" || settings.DefaultLocale != "en" || settings.Require2FA {
			t.Fatalf("settings %+v %v", settings, err)
		}
		if len(events(t, e, auth.EventSetupCompleted)) != 1 {
			t.Fatal("setup event missing")
		}
	})
}

func TestSetupToken(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{SetupToken: security.Secret("let-me-in")})
		if st, _ := e.svc.SetupStatus(t.Context()); !st.TokenRequired {
			t.Fatal("token not required")
		}
		in := auth.SetupInput{Token: "wrong", Email: "a@example.com", Name: "A", Password: adminPassword, Locale: "en", Timezone: "UTC"}
		_, err := e.svc.Setup(t.Context(), in, e.meta)
		wantCode(t, err, "setup.invalid_token")
		in.Token = "let-me-in"
		if _, err := e.svc.Setup(t.Context(), in, e.meta); err != nil {
			t.Fatal(err)
		}
	})
}

func TestLoginAndLockout(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{}).withAdmin()
		ctx := t.Context()

		sess := e.login("ADMIN@example.com ", adminPassword)
		if sess.Principal.Role != store.RoleAdmin {
			t.Fatal("wrong role")
		}
		for _, tc := range []struct{ email, password string }{
			{"nobody@example.com", adminPassword},
			{"admin@example.com", "wrong passphrase"},
		} {
			_, err := e.svc.Login(ctx, tc.email, tc.password, e.meta)
			wantCode(t, err, "auth.invalid_credentials")
		}

		// Four more failures reach the threshold of five.
		for range 4 {
			_, err := e.svc.Login(ctx, "admin@example.com", "wrong passphrase", e.meta)
			wantCode(t, err, "auth.invalid_credentials")
		}
		_, err := e.svc.Login(ctx, "admin@example.com", adminPassword, e.meta)
		wantCode(t, err, "auth.locked")
		if ae, _ := apperr.As(err); ae.RetryAfter <= 0 || ae.RetryAfter > time.Minute {
			t.Fatalf("retry after %s", ae.RetryAfter)
		}
		if len(events(t, e, auth.EventLoginLocked)) != 1 {
			t.Fatal("lock event missing")
		}

		e.clock.Advance(61 * time.Second)
		e.login("admin@example.com", adminPassword)
		// Success reset the counter: one failure does not lock again.
		_, err = e.svc.Login(ctx, "admin@example.com", "wrong passphrase", e.meta)
		wantCode(t, err, "auth.invalid_credentials")
		e.login("admin@example.com", adminPassword)
		if n := len(events(t, e, auth.EventLoginFailed)); n < 7 {
			t.Fatalf("expected failed login events, got %d", n)
		}
	})
}

func TestLockoutDurationGrows(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{}).withAdmin()
		wrong := func() error {
			_, err := e.svc.Login(t.Context(), "admin@example.com", "wrong passphrase", e.meta)
			return err
		}
		for range 5 {
			_ = wrong()
		}
		var got []time.Duration
		for range 6 {
			err := wrong()
			ae, _ := apperr.As(err)
			got = append(got, ae.RetryAfter)
			// Past the lock, the next wrong password extends it further.
			e.clock.Advance(ae.RetryAfter + time.Second)
			_ = wrong()
		}
		want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("lock durations %v, want %v", got, want)
			}
		}
	})
}

func TestSessions(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{}).withAdmin()
		ctx := t.Context()

		p, err := e.svc.AuthenticateSession(ctx, e.admin.Token)
		if err != nil || p.SessionID != e.admin.Principal.SessionID {
			t.Fatalf("authenticate: %v", err)
		}
		for _, bad := range []string{"", "garbage", strings.Repeat("a", 43)} {
			_, err := e.svc.AuthenticateSession(ctx, bad)
			wantCode(t, err, "auth.unauthenticated")
		}

		// Sliding expiry: activity keeps the session alive past the original seven days.
		for range 3 {
			e.clock.Advance(5 * 24 * time.Hour)
			if _, err := e.svc.AuthenticateSession(ctx, e.admin.Token); err != nil {
				t.Fatalf("session expired despite activity: %v", err)
			}
		}
		e.clock.Advance(7*24*time.Hour + time.Second)
		_, err = e.svc.AuthenticateSession(ctx, e.admin.Token)
		wantCode(t, err, "auth.unauthenticated")

		second := e.login("admin@example.com", adminPassword)
		third := e.login("admin@example.com", adminPassword)
		list, _ := e.svc.ListSessions(ctx, second.Principal)
		if len(list) != 2 {
			t.Fatalf("active sessions %d", len(list))
		}
		if err := e.svc.RevokeSession(ctx, second.Principal, third.Principal.SessionID, e.meta); err != nil {
			t.Fatal(err)
		}
		_, err = e.svc.AuthenticateSession(ctx, third.Token)
		wantCode(t, err, "auth.unauthenticated")
		if err := e.svc.Logout(ctx, second.Principal, e.meta); err != nil {
			t.Fatal(err)
		}
		_, err = e.svc.AuthenticateSession(ctx, second.Token)
		wantCode(t, err, "auth.unauthenticated")

		a, b := e.login("admin@example.com", adminPassword), e.login("admin@example.com", adminPassword)
		if err := e.svc.RevokeAllSessions(ctx, a.Principal, e.meta); err != nil {
			t.Fatal(err)
		}
		for _, sess := range []*auth.IssuedSession{a, b} {
			_, err := e.svc.AuthenticateSession(ctx, sess.Token)
			wantCode(t, err, "auth.unauthenticated")
		}
	})
}

func TestCSRFToken(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{}).withAdmin()
		other := e.login("admin@example.com", adminPassword)
		p := e.admin.Principal
		if !e.svc.VerifyCSRF(p, e.admin.CSRFToken) {
			t.Fatal("own token rejected")
		}
		if e.svc.VerifyCSRF(p, other.CSRFToken) || e.svc.VerifyCSRF(p, "") || e.svc.VerifyCSRF(nil, e.admin.CSRFToken) {
			t.Fatal("foreign or empty token accepted")
		}
		if e.admin.CSRFToken != e.svc.CSRFToken(p.SessionID) {
			t.Fatal("token is not stable for a session")
		}
	})
}

func enableTOTP(t *testing.T, e *env, p *auth.Principal) (secret string, codes []string) {
	t.Helper()
	enrollment, err := e.svc.BeginTOTP(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(enrollment.Secret, e.clock.Now())
	codes, err = e.svc.ConfirmTOTP(t.Context(), p, code, e.meta)
	if err != nil {
		t.Fatal(err)
	}
	return enrollment.Secret, codes
}

func TestTOTPLogin(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{}).withAdmin()
		ctx := t.Context()
		p := e.admin.Principal

		if _, err := e.svc.ConfirmTOTP(ctx, p, "123456", e.meta); err == nil {
			t.Fatal("confirm without setup")
		}
		enrollment, _ := e.svc.BeginTOTP(ctx, p)
		_, err := e.svc.ConfirmTOTP(ctx, p, "000000", e.meta)
		wantField(t, err, "code", "validation.code_invalid")
		code, _ := totp.GenerateCode(enrollment.Secret, e.clock.Now())
		codes, err := e.svc.ConfirmTOTP(ctx, p, code, e.meta)
		if err != nil || len(codes) != auth.RecoveryCodeCount {
			t.Fatalf("confirm: %v", err)
		}
		_, err = e.svc.BeginTOTP(ctx, p)
		wantCode(t, err, "totp.already_enabled")

		start := func() string {
			res, err := e.svc.Login(ctx, "admin@example.com", adminPassword, e.meta)
			if err != nil || res.Session != nil || res.ChallengeToken == "" {
				t.Fatalf("login with TOTP: %+v %v", res, err)
			}
			return res.ChallengeToken
		}

		// The code used to confirm is already spent; the next step works once.
		e.clock.Advance(30 * time.Second)
		c := start()
		_, err = e.svc.LoginSecondFactor(ctx, c, auth.SecondFactor{Code: code}, e.meta)
		wantCode(t, err, "auth.mfa_invalid")
		fresh, _ := totp.GenerateCode(enrollment.Secret, e.clock.Now())
		if _, err := e.svc.LoginSecondFactor(ctx, c, auth.SecondFactor{Code: fresh}, e.meta); err != nil {
			t.Fatalf("valid code rejected: %v", err)
		}
		_, err = e.svc.LoginSecondFactor(ctx, c, auth.SecondFactor{Code: fresh}, e.meta)
		wantCode(t, err, "auth.challenge_expired")
		_, err = e.svc.LoginSecondFactor(ctx, start(), auth.SecondFactor{Code: fresh}, e.meta)
		wantCode(t, err, "auth.mfa_invalid")

		// Recovery codes work once, with any formatting.
		if _, err := e.svc.LoginSecondFactor(ctx, start(), auth.SecondFactor{Code: strings.ToUpper(codes[0])}, e.meta); err != nil {
			t.Fatalf("recovery code: %v", err)
		}
		_, err = e.svc.LoginSecondFactor(ctx, start(), auth.SecondFactor{Code: codes[0]}, e.meta)
		wantCode(t, err, "auth.mfa_invalid")

		// A challenge expires after five minutes.
		c = start()
		e.clock.Advance(6 * time.Minute)
		_, err = e.svc.LoginSecondFactor(ctx, c, auth.SecondFactor{Code: codes[1]}, e.meta)
		wantCode(t, err, "auth.challenge_expired")
	})
}

func TestChallengeAttemptsAreLimited(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{LockoutThreshold: 100}).withAdmin()
		_, codes := enableTOTP(t, e, e.admin.Principal)
		res, _ := e.svc.Login(t.Context(), "admin@example.com", adminPassword, e.meta)
		for range 5 {
			_, err := e.svc.LoginSecondFactor(t.Context(), res.ChallengeToken, auth.SecondFactor{Code: "999999"}, e.meta)
			wantCode(t, err, "auth.mfa_invalid")
		}
		_, err := e.svc.LoginSecondFactor(t.Context(), res.ChallengeToken, auth.SecondFactor{Code: codes[0]}, e.meta)
		wantCode(t, err, "auth.challenge_expired")
	})
}

func TestTOTPManagement(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{}).withAdmin()
		p := e.admin.Principal
		_, old := enableTOTP(t, e, p)

		_, err := e.svc.RegenerateRecoveryCodes(t.Context(), p, "wrong passphrase", e.meta)
		wantField(t, err, "password", "validation.password_incorrect")
		fresh, err := e.svc.RegenerateRecoveryCodes(t.Context(), p, adminPassword, e.meta)
		if err != nil || fresh[0] == old[0] {
			t.Fatalf("regenerate: %v", err)
		}
		res, _ := e.svc.Login(t.Context(), "admin@example.com", adminPassword, e.meta)
		_, err = e.svc.LoginSecondFactor(t.Context(), res.ChallengeToken, auth.SecondFactor{Code: old[0]}, e.meta)
		wantCode(t, err, "auth.mfa_invalid")

		err = e.svc.DisableTOTP(t.Context(), p, "wrong passphrase", e.meta)
		wantField(t, err, "password", "validation.password_incorrect")
		if err := e.svc.DisableTOTP(t.Context(), p, adminPassword, e.meta); err != nil {
			t.Fatal(err)
		}
		e.login("admin@example.com", adminPassword) // no second factor any more
		wantCode(t, e.svc.DisableTOTP(t.Context(), p, adminPassword, e.meta), "totp.not_enabled")
	})
}

func TestRestrictions(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{}).withAdmin()
		ctx := t.Context()
		_, temp := e.createUser("viewer@example.com", store.RoleViewer)

		sess := e.login("viewer@example.com", temp)
		other := e.login("viewer@example.com", temp)
		if sess.Principal.Restriction != auth.RestrictionPasswordChange {
			t.Fatalf("restriction %q", sess.Principal.Restriction)
		}
		err := e.svc.ChangePassword(ctx, sess.Principal, "wrong current", "viewer passphrase 1", e.meta)
		wantField(t, err, "current_password", "validation.password_incorrect")
		err = e.svc.ChangePassword(ctx, sess.Principal, temp, "1234567890", e.meta)
		wantField(t, err, "new_password", "validation.password_common")
		if err := e.svc.ChangePassword(ctx, sess.Principal, temp, "viewer passphrase 1", e.meta); err != nil {
			t.Fatal(err)
		}
		p, err := e.svc.AuthenticateSession(ctx, sess.Token)
		if err != nil || p.Restriction != auth.RestrictionNone {
			t.Fatalf("after change: %+v %v", p, err)
		}
		_, err = e.svc.AuthenticateSession(ctx, other.Token)
		wantCode(t, err, "auth.unauthenticated")

		// Requiring 2FA restricts users without it until they enroll.
		yes := true
		if _, err := e.svc.UpdateSettings(e.wsCtx, e.admin.Principal, auth.SettingsPatch{Require2FA: &yes}, e.meta); err != nil {
			t.Fatal(err)
		}
		p, _ = e.svc.AuthenticateSession(ctx, sess.Token)
		if p.Restriction != auth.RestrictionMFASetup {
			t.Fatalf("restriction %q", p.Restriction)
		}
		enableTOTP(t, e, p)
		p, _ = e.svc.AuthenticateSession(ctx, sess.Token)
		if p.Restriction != auth.RestrictionNone {
			t.Fatalf("restriction after enrolling %q", p.Restriction)
		}
	})
}

func TestUserAdministration(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{}).withAdmin()
		ctx := e.wsCtx
		admin := e.admin.Principal

		ed, temp := e.createUser("editor@example.com", store.RoleEditor)
		if !ed.User.MustChangePassword || len(temp) != 16 || ed.User.Locale != "en" {
			t.Fatalf("created %+v", ed.User)
		}
		_, _, err := e.svc.CreateUser(ctx, admin, auth.NewUserInput{Email: "EDITOR@example.com", Name: "Dup", Role: store.RoleViewer}, e.meta)
		wantCode(t, err, "user.email_taken")
		_, _, err = e.svc.CreateUser(ctx, admin, auth.NewUserInput{Email: "x@example.com", Name: "X", Role: "owner"}, e.meta)
		wantField(t, err, "role", "validation.invalid_value")

		sess := e.login("editor@example.com", temp)
		promote := store.RoleAdmin
		updated, err := e.svc.UpdateUser(ctx, admin, ed.User.ID, auth.UserPatch{Version: ed.User.Version, Role: &promote}, e.meta)
		if err != nil || updated.Role != store.RoleAdmin {
			t.Fatalf("promote: %v", err)
		}
		_, err = e.svc.AuthenticateSession(t.Context(), sess.Token)
		wantCode(t, err, "auth.unauthenticated") // role change signs the user out
		_, err = e.svc.UpdateUser(ctx, admin, ed.User.ID, auth.UserPatch{Version: ed.User.Version, Name: ptr("stale")}, e.meta)
		if !errors.Is(err, store.ErrConflict) {
			t.Fatalf("stale version: %v", err)
		}

		// Admins cannot demote or disable themselves; the last admin cannot go.
		demote := store.RoleViewer
		me, _ := e.svc.GetUser(ctx, admin.UserID)
		_, err = e.svc.UpdateUser(ctx, admin, admin.UserID, auth.UserPatch{Version: me.User.Version, Role: &demote}, e.meta)
		wantCode(t, err, "user.self_modification")
		_, err = e.svc.UpdateUser(ctx, nil, updated.User.ID, auth.UserPatch{Version: updated.User.Version, Role: &demote}, e.meta)
		if err != nil {
			t.Fatalf("demoting one of two admins: %v", err)
		}
		me, _ = e.svc.GetUser(ctx, admin.UserID)
		_, err = e.svc.UpdateUser(ctx, nil, admin.UserID, auth.UserPatch{Version: me.User.Version, Role: &demote}, e.meta)
		wantCode(t, err, "user.last_admin")
		_, err = e.svc.UpdateUser(ctx, nil, admin.UserID, auth.UserPatch{Version: me.User.Version, Disabled: ptr(true)}, e.meta)
		wantCode(t, err, "user.last_admin")

		// Disabling signs out, revokes keys and blocks login; enabling restores login.
		v, vtemp := e.createUser("viewer@example.com", store.RoleViewer)
		vs := e.login("viewer@example.com", vtemp)
		dis, err := e.svc.UpdateUser(ctx, admin, v.User.ID, auth.UserPatch{Version: v.User.Version, Disabled: ptr(true)}, e.meta)
		if err != nil || !dis.User.Disabled() {
			t.Fatalf("disable: %v", err)
		}
		_, err = e.svc.AuthenticateSession(t.Context(), vs.Token)
		wantCode(t, err, "auth.unauthenticated")
		_, err = e.svc.Login(t.Context(), "viewer@example.com", vtemp, e.meta)
		wantCode(t, err, "auth.invalid_credentials")
		if _, err := e.svc.UpdateUser(ctx, admin, v.User.ID, auth.UserPatch{Version: dis.User.Version, Disabled: ptr(false)}, e.meta); err != nil {
			t.Fatal(err)
		}
		e.login("viewer@example.com", vtemp)

		// Reset gives a new temporary password and forces a change.
		newTemp, err := e.svc.ResetPassword(ctx, admin, v.User.ID, e.meta)
		if err != nil || newTemp == vtemp {
			t.Fatalf("reset: %v", err)
		}
		_, err = e.svc.Login(t.Context(), "viewer@example.com", vtemp, e.meta)
		wantCode(t, err, "auth.invalid_credentials")
		if s := e.login("viewer@example.com", newTemp); s.Principal.Restriction != auth.RestrictionPasswordChange {
			t.Fatal("reset password is not temporary")
		}

		for _, typ := range []string{auth.EventUserCreated, auth.EventUserRoleChanged, auth.EventUserDisabled, auth.EventUserEnabled, auth.EventPasswordReset} {
			if len(events(t, e, typ)) == 0 {
				t.Errorf("no %s event", typ)
			}
		}
	})
}

func ptr[T any](v T) *T { return &v }

func TestAdminDisablesTOTP(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{}).withAdmin()
		u, temp := e.createUser("lost@example.com", store.RoleEditor)
		sess := e.login("lost@example.com", temp)
		enableTOTP(t, e, sess.Principal)
		if err := e.svc.AdminDisableTOTP(e.wsCtx, e.admin.Principal, u.User.ID, e.meta); err != nil {
			t.Fatal(err)
		}
		e.login("lost@example.com", temp)
	})
}

func TestAPIKeys(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{}).withAdmin()
		ctx := e.wsCtx
		admin := e.admin.Principal

		_, err := e.svc.CreateAPIKey(ctx, admin, auth.NewAPIKeyInput{Name: "", Scopes: []auth.Scope{"root"}}, e.meta)
		wantField(t, err, "name", "validation.required")
		wantField(t, err, "scopes", "validation.invalid_value")
		past := e.clock.Now().Add(-time.Hour)
		_, err = e.svc.CreateAPIKey(ctx, admin, auth.NewAPIKeyInput{Name: "x", Scopes: []auth.Scope{"read"}, ExpiresAt: &past}, e.meta)
		wantField(t, err, "expires_at", "validation.invalid_value")

		expires := e.clock.Now().Add(24 * time.Hour)
		created, err := e.svc.CreateAPIKey(ctx, admin, auth.NewAPIKeyInput{Name: "ci", Scopes: []auth.Scope{"read", "run", "read"}, ExpiresAt: &expires}, e.meta)
		if err != nil || !strings.HasPrefix(created.Plaintext, "rbk_"+created.Key.Prefix) || len(created.Key.Scopes) != 2 {
			t.Fatalf("create: %+v %v", created, err)
		}
		p, err := e.svc.AuthenticateAPIKey(t.Context(), created.Plaintext)
		if err != nil || !p.IsAPIKey() || p.Role != store.RoleAdmin || !p.HasScope(auth.ScopeRun) || p.HasScope(auth.ScopeWrite) {
			t.Fatalf("authenticate: %+v %v", p, err)
		}
		// Change the last character to a different one (the key may already end in "x").
		last := "x"
		if strings.HasSuffix(created.Plaintext, "x") {
			last = "y"
		}
		_, err = e.svc.AuthenticateAPIKey(t.Context(), created.Plaintext[:len(created.Plaintext)-1]+last)
		wantCode(t, err, "auth.unauthenticated")

		e.clock.Advance(25 * time.Hour)
		_, err = e.svc.AuthenticateAPIKey(t.Context(), created.Plaintext)
		wantCode(t, err, "auth.unauthenticated") // expired

		k2, _ := e.svc.CreateAPIKey(ctx, admin, auth.NewAPIKeyInput{Name: "k2", Scopes: []auth.Scope{"admin"}}, e.meta)
		if _, err := e.svc.RevokeAPIKey(ctx, admin, k2.Key.ID, e.meta); err != nil {
			t.Fatal(err)
		}
		_, err = e.svc.AuthenticateAPIKey(t.Context(), k2.Plaintext)
		wantCode(t, err, "auth.unauthenticated")
		if _, err := e.svc.RevokeAPIKey(ctx, admin, k2.Key.ID, e.meta); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("second revoke: %v", err)
		}

		// A key of a disabled owner stops working.
		other, temp := e.createUser("admin2@example.com", store.RoleAdmin)
		os := e.login("admin2@example.com", temp)
		k3, _ := e.svc.CreateAPIKey(ctx, os.Principal, auth.NewAPIKeyInput{Name: "k3", Scopes: []auth.Scope{"write"}}, e.meta)
		if _, err := e.svc.UpdateUser(ctx, admin, other.User.ID, auth.UserPatch{Version: other.User.Version, Disabled: ptr(true)}, e.meta); err != nil {
			t.Fatal(err)
		}
		_, err = e.svc.AuthenticateAPIKey(t.Context(), k3.Plaintext)
		wantCode(t, err, "auth.unauthenticated")
	})
}

// TestSecretsNeverLeak runs the flows that handle secrets and checks that no secret appears in
// logs, security events or error messages (docs/spec/07-security.md).
func TestSecretsNeverLeak(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{}).withAdmin()
		ctx := t.Context()
		var secrets []string
		var errs []string
		collect := func(err error) {
			if err != nil {
				errs = append(errs, err.Error())
			}
		}

		secrets = append(secrets, adminPassword, e.admin.Token, e.admin.CSRFToken)
		_, err := e.svc.Login(ctx, "admin@example.com", "wrong secret passphrase", e.meta)
		collect(err)
		secrets = append(secrets, "wrong secret passphrase")

		totpSecret, codes := enableTOTP(t, e, e.admin.Principal)
		secrets = append(secrets, totpSecret)
		secrets = append(secrets, codes...)
		res, _ := e.svc.Login(ctx, "admin@example.com", adminPassword, e.meta)
		secrets = append(secrets, res.ChallengeToken)
		_, err = e.svc.LoginSecondFactor(ctx, res.ChallengeToken, auth.SecondFactor{Code: "000000"}, e.meta)
		collect(err)
		sess, err := e.svc.LoginSecondFactor(ctx, res.ChallengeToken, auth.SecondFactor{Code: codes[0]}, e.meta)
		if err != nil {
			t.Fatal(err)
		}
		secrets = append(secrets, sess.Token)

		u, temp := e.createUser("v@example.com", store.RoleViewer)
		secrets = append(secrets, temp)
		reset, _ := e.svc.ResetPassword(e.wsCtx, e.admin.Principal, u.User.ID, e.meta)
		secrets = append(secrets, reset)
		key, _ := e.svc.CreateAPIKey(e.wsCtx, e.admin.Principal, auth.NewAPIKeyInput{Name: "k", Scopes: []auth.Scope{"read"}}, e.meta)
		secrets = append(secrets, key.Plaintext)
		_, err = e.svc.AuthenticateAPIKey(ctx, key.Plaintext+"x")
		collect(err)
		collect(e.svc.ChangePassword(ctx, sess.Principal, "not my password!", "brand new passphrase", e.meta))
		secrets = append(secrets, "not my password!", "brand new passphrase")

		page, _ := e.svc.ListSecurityEvents(e.wsCtx, store.SecurityEventFilter{}, store.PageRequest{Limit: 200})
		eventsJSON, _ := json.Marshal(page.Items)
		haystacks := map[string]string{"logs": e.logs.String(), "events": string(eventsJSON), "errors": strings.Join(errs, "\n")}
		for where, text := range haystacks {
			for _, secret := range secrets {
				if secret != "" && strings.Contains(text, secret) {
					t.Errorf("%s contain a secret (%d chars)", where, len(secret))
				}
			}
		}
		if len(page.Items) < 8 {
			t.Fatalf("expected a trail of events, got %d", len(page.Items))
		}
	})
}
