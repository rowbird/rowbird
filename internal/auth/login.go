package auth

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/store"
)

// IssuedSession is a freshly created browser session. Token and CSRFToken go to the client as
// cookies and are never stored or logged.
type IssuedSession struct {
	Token     string
	CSRFToken string
	ExpiresAt time.Time
	Principal *Principal
}

// LoginResult is either a session or, when the account has a second factor, a challenge token for
// LoginSecondFactor.
type LoginResult struct {
	Session        *IssuedSession
	ChallengeToken string
	// Methods lists the second factors the user can finish with ("totp", "passkey").
	Methods []string
}

// Login checks email and password. Every failure looks the same to the caller except a locked
// account, which reports when to retry.
func (s *Service) Login(ctx context.Context, email, password string, meta RequestMeta) (*LoginResult, error) {
	now := s.clock()
	user, err := s.store.Users().GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if err != nil || user.Disabled() || user.PasswordHash == nil {
		s.hasher.VerifyDummy(ctx, password)
		s.recordAnonymous(ctx, EventLoginFailed, meta, map[string]any{"email": store.NormalizeEmail(email), "reason": "unknown_or_disabled"})
		return nil, ErrInvalidCredentials
	}
	wsCtx, err := s.userWorkspace(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if user.LockedUntil != nil && now.Before(*user.LockedUntil) {
		_ = s.record(wsCtx, &user.ID, EventLoginFailed, meta.IP, map[string]any{"reason": "locked"})
		return nil, lockedError(user.LockedUntil.Sub(now))
	}

	ok, rehash, err := s.hasher.Verify(ctx, password, *user.PasswordHash)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, s.registerFailure(wsCtx, user, meta, "password")
	}
	if rehash {
		s.upgradeHash(ctx, user, password)
	}

	methods, err := s.secondFactorMethods(ctx, user)
	if err != nil {
		return nil, err
	}
	if len(methods) > 0 {
		token, hash := NewSessionToken()
		c := &store.LoginChallenge{UserID: user.ID, TokenHash: hash, ExpiresAt: now.Add(s.cfg.ChallengeTTL)}
		if err := s.store.Auth().CreateChallenge(ctx, c); err != nil {
			return nil, err
		}
		return &LoginResult{ChallengeToken: token, Methods: methods}, nil
	}
	sess, err := s.completeLogin(ctx, wsCtx, user, meta, "password")
	if err != nil {
		return nil, err
	}
	return &LoginResult{Session: sess}, nil
}

var sixDigits = regexp.MustCompile(`^\d{6}$`)

// SecondFactor is the answer to a login challenge: a TOTP or recovery code, or a passkey assertion.
type SecondFactor struct {
	Code    string
	Passkey json.RawMessage
}

// LoginSecondFactor finishes a login with a TOTP code, a recovery code or a passkey. Failures count
// toward the account lockout, and a challenge allows a limited number of attempts.
func (s *Service) LoginSecondFactor(ctx context.Context, challengeToken string, answer SecondFactor, meta RequestMeta) (*IssuedSession, error) {
	now := s.clock()
	c, err := s.store.Auth().ChallengeByTokenHash(ctx, HashToken(challengeToken))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrChallengeExpired
	}
	if err != nil {
		return nil, err
	}
	if !now.Before(c.ExpiresAt) || c.Attempts >= s.cfg.ChallengeMaxAttempts {
		_ = s.store.Auth().DeleteChallenge(ctx, c, now)
		return nil, ErrChallengeExpired
	}
	user, err := s.store.Users().Get(ctx, c.UserID)
	if err != nil {
		return nil, err
	}
	wsCtx, err := s.userWorkspace(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	has2FA, err := s.hasSecondFactor(ctx, user)
	if err != nil {
		return nil, err
	}
	if user.Disabled() || !has2FA {
		_ = s.store.Auth().DeleteChallenge(ctx, c, now)
		return nil, ErrChallengeExpired
	}
	if user.LockedUntil != nil && now.Before(*user.LockedUntil) {
		return nil, lockedError(user.LockedUntil.Sub(now))
	}

	var method string
	var ok bool
	if len(answer.Passkey) > 0 {
		method = "passkey"
		ok, err = s.checkPasskeySecondFactor(ctx, user, c.TokenHash, answer.Passkey)
	} else {
		method, ok, err = s.checkSecondFactor(wsCtx, user, answer.Code, meta)
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		attempts, err := s.store.Auth().IncrementChallengeAttempts(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		if attempts >= s.cfg.ChallengeMaxAttempts {
			_ = s.store.Auth().DeleteChallenge(ctx, c, now)
		}
		if err := s.registerFailure(wsCtx, user, meta, "second_factor"); !errors.Is(err, ErrInvalidCredentials) {
			return nil, err
		}
		return nil, ErrMFAInvalid
	}
	if err := s.store.Auth().DeleteChallenge(ctx, c, now); err != nil {
		return nil, err
	}
	return s.completeLogin(ctx, wsCtx, user, meta, method)
}

// checkSecondFactor accepts a TOTP code (six digits) or an unused recovery code.
func (s *Service) checkSecondFactor(wsCtx context.Context, user *store.User, code string, meta RequestMeta) (method string, ok bool, err error) {
	if sixDigits.MatchString(code) {
		secret, err := s.totpSecret(user)
		if err != nil {
			return "", false, err
		}
		step, match := MatchTOTP(secret, code, s.clock())
		if !match {
			return "totp", false, nil
		}
		fresh, err := s.store.Users().UseTOTPStep(wsCtx, user.ID, step)
		return "totp", fresh, err
	}

	idx := MatchRecoveryCode(code, user.RecoveryCodes)
	if idx < 0 {
		return "recovery_code", false, nil
	}
	user.RecoveryCodes = append(user.RecoveryCodes[:idx:idx], user.RecoveryCodes[idx+1:]...)
	if err := s.store.Users().Update(wsCtx, user, "recovery_codes"); err != nil {
		// A concurrent use of the same code loses the race and fails.
		if errors.Is(err, store.ErrConflict) {
			return "recovery_code", false, nil
		}
		return "", false, err
	}
	_ = s.record(wsCtx, &user.ID, EventRecoveryCodeUsed, meta.IP, map[string]any{"remaining": len(user.RecoveryCodes)})
	return "recovery_code", true, nil
}

// registerFailure counts a failed attempt, locks the account once the threshold is reached and
// always returns ErrInvalidCredentials.
func (s *Service) registerFailure(wsCtx context.Context, user *store.User, meta RequestMeta, reason string) error {
	n, err := s.store.Users().IncrementFailedLogins(wsCtx, user.ID)
	if err != nil {
		return err
	}
	_ = s.record(wsCtx, &user.ID, EventLoginFailed, meta.IP, map[string]any{"reason": reason})
	if n >= s.cfg.LockoutThreshold {
		d := s.lockoutDuration(n)
		until := s.clock().Add(d)
		if err := s.store.Users().SetLockedUntil(wsCtx, user.ID, &until); err != nil {
			return err
		}
		_ = s.record(wsCtx, &user.ID, EventLoginLocked, meta.IP, map[string]any{"seconds": int(d.Seconds())})
	}
	return ErrInvalidCredentials
}

// lockoutDuration doubles from LockoutBase for every failure past the threshold, up to LockoutMax.
func (s *Service) lockoutDuration(failures int) time.Duration {
	d := s.cfg.LockoutBase
	for i := s.cfg.LockoutThreshold; i < failures && d < s.cfg.LockoutMax; i++ {
		d *= 2
	}
	return min(d, s.cfg.LockoutMax)
}

func (s *Service) completeLogin(ctx, wsCtx context.Context, user *store.User, meta RequestMeta, method string) (*IssuedSession, error) {
	if err := s.store.Users().RecordLogin(ctx, user.ID, s.clock()); err != nil {
		return nil, err
	}
	sess, err := s.startSession(ctx, user, meta, method)
	if err != nil {
		return nil, err
	}
	_ = s.record(wsCtx, &user.ID, EventLoginSuccess, meta.IP, map[string]any{"method": method})
	return sess, nil
}

func (s *Service) upgradeHash(ctx context.Context, user *store.User, password string) {
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return
	}
	user.PasswordHash = &hash
	if err := s.store.Users().Update(ctx, user, "password_hash"); err != nil {
		s.logger.WarnContext(ctx, "could not upgrade password hash", "user_id", user.ID.String(), "error", err)
	}
}

// startSession creates a session in the user's workspace.
// method is how the user signed in: password (with any second factor), passkey or oidc.
func (s *Service) startSession(ctx context.Context, user *store.User, meta RequestMeta, method string) (*IssuedSession, error) {
	wsCtx, err := s.userWorkspace(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	wsID, _ := store.WorkspaceFrom(wsCtx)
	now := s.clock()
	token, hash := NewSessionToken()
	sess := &store.Session{
		UserID: user.ID, TokenHash: hash, ExpiresAt: now.Add(s.cfg.SessionTTL), LastSeenAt: now,
		IP: meta.IP, UserAgent: truncate(meta.UserAgent, 512), AuthMethod: sessionMethod(method),
	}
	sess.WorkspaceID = wsID
	if err := s.store.Auth().CreateSession(ctx, sess); err != nil {
		return nil, err
	}
	p, err := s.principalForSession(wsCtx, sess, user)
	if err != nil {
		return nil, err
	}
	return &IssuedSession{Token: token, CSRFToken: s.CSRFToken(sess.ID), ExpiresAt: sess.ExpiresAt, Principal: p}, nil
}

// AuthenticateSession resolves a session cookie. Any problem is ErrUnauthenticated.
func (s *Service) AuthenticateSession(ctx context.Context, token string) (*Principal, error) {
	if token == "" {
		return nil, ErrUnauthenticated
	}
	now := s.clock()
	sess, err := s.store.Auth().SessionByTokenHash(ctx, HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if !sess.Active(now) {
		return nil, ErrUnauthenticated
	}
	user, err := s.store.Users().Get(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	if user.Disabled() {
		return nil, ErrUnauthenticated
	}
	if now.Sub(sess.LastSeenAt) >= s.cfg.SessionTouchInterval {
		if err := s.store.Auth().TouchSession(ctx, sess.ID, now, now.Add(s.cfg.SessionTTL)); err != nil {
			return nil, err
		}
	}
	return s.principalForSession(store.WithWorkspace(ctx, sess.WorkspaceID), sess, user)
}

func (s *Service) principalForSession(wsCtx context.Context, sess *store.Session, user *store.User) (*Principal, error) {
	m, err := s.store.Members().GetByUser(wsCtx, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	p := &Principal{UserID: user.ID, WorkspaceID: sess.WorkspaceID, Role: m.Role, SessionID: sess.ID}
	switch {
	case sess.AuthMethod == SessionOIDC:
		// The provider authenticated the user; its password and second factor are its business.
	case user.MustChangePassword:
		p.Restriction = RestrictionPasswordChange
	case !user.TOTPEnabled:
		required, err := s.require2FA(wsCtx)
		if err != nil {
			return nil, err
		}
		if required {
			has2FA, err := s.hasSecondFactor(wsCtx, user)
			if err != nil {
				return nil, err
			}
			if !has2FA {
				p.Restriction = RestrictionMFASetup
			}
		}
	}
	return p, nil
}

// AuthenticateAPIKey resolves a bearer API key. The key is valid only while it is active and its
// owner is an enabled member of the key's workspace; the owner's current role still applies.
func (s *Service) AuthenticateAPIKey(ctx context.Context, key string) (*Principal, error) {
	if !LooksLikeAPIKey(key) {
		return nil, ErrUnauthenticated
	}
	now := s.clock()
	k, err := s.store.Auth().APIKeyByHash(ctx, HashToken(key))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if !k.Active(now) {
		return nil, ErrUnauthenticated
	}
	user, err := s.store.Users().Get(ctx, k.UserID)
	if err != nil {
		return nil, err
	}
	if user.Disabled() {
		return nil, ErrUnauthenticated
	}
	wsCtx := store.WithWorkspace(ctx, k.WorkspaceID)
	m, err := s.store.Members().GetByUser(wsCtx, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if k.LastUsedAt == nil || now.Sub(*k.LastUsedAt) >= s.cfg.SessionTouchInterval {
		if err := s.store.Auth().TouchAPIKey(ctx, k.ID, now); err != nil {
			return nil, err
		}
	}
	scopes := make([]Scope, len(k.Scopes))
	for i, sc := range k.Scopes {
		scopes[i] = Scope(sc)
	}
	return &Principal{UserID: user.ID, WorkspaceID: k.WorkspaceID, Role: m.Role, APIKeyID: k.ID, Scopes: scopes}, nil
}

// Logout revokes the current session.
func (s *Service) Logout(ctx context.Context, p *Principal, meta RequestMeta) error {
	if p.SessionID == uuid.Nil {
		return nil
	}
	err := s.store.Auth().RevokeSession(ctx, p.UserID, p.SessionID, s.clock())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	_ = s.record(p.Context(ctx), &p.UserID, EventLogout, meta.IP, nil)
	return nil
}

// userWorkspace binds ctx to the user's workspace. The OSS edition has one membership per user.
func (s *Service) userWorkspace(ctx context.Context, userID uuid.UUID) (context.Context, error) {
	ms, err := s.store.Auth().MembershipsOfUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, ErrInvalidCredentials
	}
	return store.WithWorkspace(ctx, ms[0].WorkspaceID), nil
}

// recordAnonymous records an event without a known user in the default workspace, if it exists.
func (s *Service) recordAnonymous(ctx context.Context, typ string, meta RequestMeta, data map[string]any) {
	ws, err := s.store.Workspaces().GetBySlug(ctx, DefaultWorkspaceSlug)
	if err != nil {
		return
	}
	_ = s.record(store.WithWorkspace(ctx, ws.ID), nil, typ, meta.IP, data)
}

func (s *Service) totpSecret(user *store.User) (string, error) {
	if user.TOTPSecretEnc == nil {
		return "", ErrTOTPNotEnabled
	}
	b, err := s.keyring.Decrypt(*user.TOTPSecretEnc, TOTPAssociatedData(user.ID))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// TOTPAssociatedData binds a user's encrypted TOTP secret to that user.
func TOTPAssociatedData(userID uuid.UUID) []byte { return []byte("user:" + userID.String() + ":totp") }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Session authentication methods (sessions.auth_method).
const (
	SessionPassword = "password"
	SessionPasskey  = "passkey"
	SessionOIDC     = "oidc"
)

// sessionMethod maps a login method (totp and recovery codes finish a password login) to the
// method stored on the session.
func sessionMethod(method string) string {
	switch method {
	case SessionPasskey, SessionOIDC:
		return method
	}
	return SessionPassword
}
