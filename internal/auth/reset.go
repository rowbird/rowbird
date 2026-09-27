package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/store"
)

// "Forgot password" (docs/spec/07-security.md, "Authentication"): a one-use link sent by email
// through the workspace's system mailer. The request always gets the same answer, so it does not
// tell whether an account exists, and the email is sent in the background so the timing does not
// tell either.

const (
	// ResetTokenPrefix marks reset tokens, like rbk_ for API keys and rbl_ for links.
	ResetTokenPrefix = "rbr_"
	resetTTL         = 30 * time.Minute
	resetInterval    = 5 * time.Minute
	// ResetPath is the page the emailed link opens.
	ResetPath = "/reset-password"
)

// EventPasswordResetAsked records a "forgot password" request, with whether an email was sent.
const EventPasswordResetAsked = "password_reset_requested"

// ErrResetInvalid is a reset link that is unknown, used or expired.
var ErrResetInvalid = apperr.New(apperr.KindGone, "auth.reset_invalid")

// ResetMailer sends the reset link to a user; the notification service implements it.
type ResetMailer interface {
	SendPasswordReset(ctx context.Context, user *store.User, link string) error
	// PasswordResetAvailable reports whether the workspace in ctx can send system email.
	PasswordResetAvailable(ctx context.Context) bool
}

type resetState struct {
	mu     sync.Mutex
	mailer ResetMailer
	wg     sync.WaitGroup
}

// SetResetMailer wires the sender of reset links; without one, "forgot password" is off.
func (s *Service) SetResetMailer(m ResetMailer) {
	s.reset.mu.Lock()
	s.reset.mailer = m
	s.reset.mu.Unlock()
}

func (s *Service) resetMailer() ResetMailer {
	s.reset.mu.Lock()
	defer s.reset.mu.Unlock()
	return s.reset.mailer
}

// WaitResetMails waits for reset emails being sent in the background (tests and shutdown).
func (s *Service) WaitResetMails() { s.reset.wg.Wait() }

// PasswordResetAvailable tells the login page whether to offer "forgot password": it needs the
// public URL for the link and a system mailer in the default workspace.
func (s *Service) PasswordResetAvailable(ctx context.Context) bool {
	m := s.resetMailer()
	if m == nil || s.cfg.BaseURL == "" {
		return false
	}
	ws, err := s.store.Workspaces().GetBySlug(ctx, DefaultWorkspaceSlug)
	if err != nil {
		return false
	}
	return m.PasswordResetAvailable(store.WithWorkspace(ctx, ws.ID))
}

// RequestPasswordReset emails a reset link when the email belongs to an active user with a
// password, at most once every five minutes per account. It answers the same in every case; only
// storage failures are errors.
func (s *Service) RequestPasswordReset(ctx context.Context, email string, meta RequestMeta) error {
	if !s.PasswordResetAvailable(ctx) {
		return nil
	}
	user, err := s.store.Users().GetByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		s.recordAnonymous(ctx, EventPasswordResetAsked, meta, map[string]any{"email": store.NormalizeEmail(email), "sent": false})
		return nil
	}
	if err != nil {
		return err
	}
	wsCtx, err := s.userWorkspace(ctx, user.ID)
	if err != nil || user.Disabled() || user.PasswordHash == nil {
		return nil //nolint:nilerr // no reset for users without a workspace, disabled or OIDC-only; same answer
	}
	now := s.clock()
	if last, err := s.store.Auth().LatestPasswordReset(ctx, user.ID); err != nil {
		return err
	} else if last != nil && now.Sub(*last) < resetInterval {
		return nil
	}
	body, _ := NewSessionToken()
	token := ResetTokenPrefix + body
	hash := HashToken(token)
	pr := &store.PasswordReset{UserID: user.ID, TokenHash: hash, ExpiresAt: now.Add(resetTTL)}
	pr.CreatedAt = now // the throttle compares it with this service's clock
	if err := s.store.Auth().ReplacePasswordReset(ctx, pr); err != nil {
		return err
	}
	_ = s.record(wsCtx, &user.ID, EventPasswordResetAsked, meta.IP, map[string]any{"sent": true})
	link := strings.TrimRight(s.cfg.BaseURL, "/") + ResetPath + "?token=" + token
	m := s.resetMailer()
	u := *user
	s.reset.wg.Go(func() {
		// Sent in the background, so the answer takes the same time whether or not the account exists.
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(wsCtx), time.Minute)
		defer cancel()
		if err := m.SendPasswordReset(sendCtx, &u, link); err != nil {
			s.logger.WarnContext(sendCtx, "could not send the password reset email", "user_id", u.ID.String(), "error", err)
		}
	})
	return nil
}

// ConfirmPasswordReset sets a new password with a reset link. It unlocks the account, drops the
// temporary-password flag and signs the user out everywhere; it does not sign in, and a second
// factor is still asked at the next sign-in.
func (s *Service) ConfirmPasswordReset(ctx context.Context, token, password string, meta RequestMeta) error {
	if !strings.HasPrefix(token, ResetTokenPrefix) {
		return ErrResetInvalid
	}
	now := s.clock()
	var hash string
	return s.store.RunInTx(ctx, func(ctx context.Context) error {
		pr, err := s.store.Auth().UsePasswordReset(ctx, HashToken(token), now)
		if errors.Is(err, store.ErrNotFound) {
			return ErrResetInvalid
		}
		if err != nil {
			return err
		}
		u, err := s.store.Users().Get(ctx, pr.UserID)
		if err != nil {
			return err
		}
		if u.Disabled() {
			return ErrResetInvalid
		}
		var fe fieldErrors
		fe.password("password", password, u.Email)
		if err := fe.err(); err != nil {
			return err
		}
		if hash, err = s.hasher.Hash(ctx, password); err != nil {
			return err
		}
		u.PasswordHash, u.MustChangePassword, u.FailedLogins, u.LockedUntil = &hash, false, 0, nil
		if err := s.store.Users().Update(ctx, u, "password_hash", "must_change_password", "failed_logins", "locked_until"); err != nil {
			return err
		}
		if _, err := s.store.Auth().RevokeUserSessions(ctx, u.ID, uuid.Nil, now); err != nil {
			return err
		}
		wsCtx, err := s.userWorkspace(ctx, u.ID)
		if err != nil {
			return err
		}
		return s.record(wsCtx, &u.ID, EventPasswordReset, meta.IP, map[string]any{"user_id": u.ID.String(), "by": "self"})
	})
}
