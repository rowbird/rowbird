package auth_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

type fakeResetMailer struct {
	mu        sync.Mutex
	available bool
	links     map[string]string // email -> last link
}

func (m *fakeResetMailer) PasswordResetAvailable(context.Context) bool { return m.available }

func (m *fakeResetMailer) SendPasswordReset(_ context.Context, u *store.User, link string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links[u.Email] = link
	return nil
}

func (m *fakeResetMailer) token(t *testing.T, email string) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	u, err := url.Parse(m.links[email])
	if err != nil || u.Path != auth.ResetPath {
		t.Fatalf("link %q", m.links[email])
	}
	return u.Query().Get("token")
}

func TestPasswordReset(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		e := newEnv(t, s, auth.Config{BaseURL: origin}).withAdmin()
		mail := &fakeResetMailer{links: map[string]string{}}
		if e.svc.PasswordResetAvailable(t.Context()) {
			t.Fatal("available without a mailer")
		}
		e.svc.SetResetMailer(mail)
		if e.svc.PasswordResetAvailable(t.Context()) {
			t.Fatal("available without a system mailer channel")
		}
		mail.available = true
		if !e.svc.PasswordResetAvailable(t.Context()) {
			t.Fatal("not available")
		}

		// Unknown accounts get the same answer and no email.
		if err := e.svc.RequestPasswordReset(t.Context(), "nobody@example.com", e.meta); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.RequestPasswordReset(t.Context(), "ADMIN@example.com", e.meta); err != nil {
			t.Fatal(err)
		}
		e.svc.WaitResetMails()
		if len(mail.links) != 1 {
			t.Fatalf("emails %v", mail.links)
		}
		first := mail.token(t, "admin@example.com")
		if !strings.HasPrefix(first, auth.ResetTokenPrefix) || !strings.HasPrefix(mail.links["admin@example.com"], origin+"/reset-password?token=") {
			t.Fatalf("link %s", mail.links["admin@example.com"])
		}

		// A second request within five minutes sends nothing; after that it replaces the link.
		if err := e.svc.RequestPasswordReset(t.Context(), "admin@example.com", e.meta); err != nil {
			t.Fatal(err)
		}
		e.svc.WaitResetMails()
		if mail.token(t, "admin@example.com") != first {
			t.Fatal("a second email within five minutes")
		}
		e.clock.Advance(6 * time.Minute)
		_ = e.svc.RequestPasswordReset(t.Context(), "admin@example.com", e.meta)
		e.svc.WaitResetMails()
		second := mail.token(t, "admin@example.com")
		if second == first {
			t.Fatal("no new link")
		}
		if err := e.svc.ConfirmPasswordReset(t.Context(), first, "a brand new passphrase", e.meta); !errors.Is(err, auth.ErrResetInvalid) {
			t.Fatalf("replaced link: %v", err)
		}

		// A weak password keeps the link usable.
		err := e.svc.ConfirmPasswordReset(t.Context(), second, "short", e.meta)
		wantField(t, err, "password", auth.CodeTooShort)
		// The failed attempt rolled back, so the link is still unused.
		if err := e.svc.ConfirmPasswordReset(t.Context(), second, "a brand new passphrase", e.meta); err != nil {
			t.Fatalf("confirm: %v", err)
		}
		if err := e.svc.ConfirmPasswordReset(t.Context(), second, "another new passphrase", e.meta); !errors.Is(err, auth.ErrResetInvalid) {
			t.Fatalf("second use: %v", err)
		}
		// Signed out everywhere; the new password works, the old one does not.
		if _, err := e.svc.AuthenticateSession(t.Context(), e.admin.Token); err == nil {
			t.Fatal("the old session survived")
		}
		if _, err := e.svc.Login(t.Context(), "admin@example.com", adminPassword, e.meta); err == nil {
			t.Fatal("the old password still works")
		}
		e.login("admin@example.com", "a brand new passphrase")
		if len(events(t, e, auth.EventPasswordReset)) == 0 {
			t.Fatal("no password_reset event")
		}

		// Links expire after 30 minutes.
		e.clock.Advance(6 * time.Minute)
		_ = e.svc.RequestPasswordReset(t.Context(), "admin@example.com", e.meta)
		e.svc.WaitResetMails()
		late := mail.token(t, "admin@example.com")
		e.clock.Advance(31 * time.Minute)
		if err := e.svc.ConfirmPasswordReset(t.Context(), late, "yet another passphrase", e.meta); !errors.Is(err, auth.ErrResetInvalid) {
			t.Fatalf("expired: %v", err)
		}
		if err := e.svc.ConfirmPasswordReset(t.Context(), "not-a-token", "yet another passphrase", e.meta); !errors.Is(err, auth.ErrResetInvalid) {
			t.Fatalf("garbage: %v", err)
		}
	})
}
