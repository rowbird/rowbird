package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/security"
	"github.com/rowbird/rowbird/internal/store"
)

// Config tunes the service. Zero values fall back to DefaultConfig.
type Config struct {
	SessionTTL           time.Duration
	SessionTouchInterval time.Duration
	ChallengeTTL         time.Duration
	ChallengeMaxAttempts int
	LockoutThreshold     int
	LockoutBase          time.Duration
	LockoutMax           time.Duration
	TOTPIssuer           string
	// SetupToken, when set, must be presented to the setup wizard.
	SetupToken security.Secret
	// BaseURL is the public URL; passkeys use its host as the relying party and need it.
	BaseURL string
}

// DefaultConfig holds the values from docs/spec/07-security.md.
var DefaultConfig = Config{
	SessionTTL:           7 * 24 * time.Hour,
	SessionTouchInterval: time.Minute,
	ChallengeTTL:         5 * time.Minute,
	ChallengeMaxAttempts: 5,
	LockoutThreshold:     5,
	LockoutBase:          time.Minute,
	LockoutMax:           15 * time.Minute,
	TOTPIssuer:           "Rowbird",
}

func (c Config) withDefaults() Config {
	d := DefaultConfig
	if c.SessionTTL > 0 {
		d.SessionTTL = c.SessionTTL
	}
	if c.SessionTouchInterval > 0 {
		d.SessionTouchInterval = c.SessionTouchInterval
	}
	if c.ChallengeTTL > 0 {
		d.ChallengeTTL = c.ChallengeTTL
	}
	if c.ChallengeMaxAttempts > 0 {
		d.ChallengeMaxAttempts = c.ChallengeMaxAttempts
	}
	if c.LockoutThreshold > 0 {
		d.LockoutThreshold = c.LockoutThreshold
	}
	if c.LockoutBase > 0 {
		d.LockoutBase = c.LockoutBase
	}
	if c.LockoutMax > 0 {
		d.LockoutMax = c.LockoutMax
	}
	if c.TOTPIssuer != "" {
		d.TOTPIssuer = c.TOTPIssuer
	}
	d.SetupToken = c.SetupToken
	d.BaseURL = c.BaseURL
	return d
}

// Service implements identity and access on top of the store.
type Service struct {
	store    *store.Store
	keyring  *crypto.Keyring
	hasher   *PasswordHasher
	cfg      Config
	csrfKey  []byte
	webauthn *webauthn.WebAuthn
	// httpClient carries OIDC requests (discovery, token exchange, keys); nil uses the default.
	httpClient *http.Client
	reset      resetState
	logger     *slog.Logger
	now        func() time.Time
}

// Options carry optional collaborators; tests use them to control time and hashing cost.
type Options struct {
	Hasher *PasswordHasher
	Now    func() time.Time
	Logger *slog.Logger
	// HTTPClient carries the requests to the OIDC provider, through the network policy.
	HTTPClient *http.Client
}

// NewService builds the service.
func NewService(st *store.Store, keyring *crypto.Keyring, cfg Config, opts Options) *Service {
	s := &Service{
		store:      st,
		keyring:    keyring,
		hasher:     opts.Hasher,
		cfg:        cfg.withDefaults(),
		csrfKey:    keyring.DeriveKey("csrf"),
		webauthn:   newWebAuthn(cfg.BaseURL),
		httpClient: opts.HTTPClient,
		logger:     opts.Logger,
		now:        opts.Now,
	}
	if s.hasher == nil {
		s.hasher = NewPasswordHasher(DefaultArgon2Params, 4)
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

func (s *Service) clock() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

// RequestMeta describes where a request came from, for sessions and security events.
type RequestMeta struct {
	IP        string
	UserAgent string
}

// Scope is an API key scope. Scopes are ordered: each one includes the ones before it.
type Scope string

// Scopes, from least to most privileged.
const (
	ScopeRead  Scope = "read"
	ScopeRun   Scope = "run"
	ScopeWrite Scope = "write"
	ScopeAdmin Scope = "admin"
)

// AllScopes lists every scope in order.
var AllScopes = []Scope{ScopeRead, ScopeRun, ScopeWrite, ScopeAdmin}

// Rank orders scopes; unknown scopes rank 0 and satisfy nothing.
func (sc Scope) Rank() int {
	for i, s := range AllScopes {
		if s == sc {
			return i + 1
		}
	}
	return 0
}

// RoleRank orders roles; unknown roles rank 0 and satisfy nothing.
func RoleRank(r store.Role) int {
	switch r {
	case store.RoleViewer:
		return 1
	case store.RoleEditor:
		return 2
	case store.RoleAdmin:
		return 3
	}
	return 0
}

// Restriction limits what a session may do until the user fixes their account.
type Restriction string

// Restrictions, in the order they are resolved.
const (
	RestrictionNone           Restriction = ""
	RestrictionPasswordChange Restriction = "password_change_required"
	RestrictionMFASetup       Restriction = "mfa_setup_required"
)

// Principal is the authenticated caller of a request.
type Principal struct {
	UserID      uuid.UUID
	WorkspaceID uuid.UUID
	Role        store.Role
	// Session is set for browser sessions.
	SessionID uuid.UUID
	// APIKeyID and Scopes are set for API keys. Sessions implicitly hold every scope.
	APIKeyID    uuid.UUID
	Scopes      []Scope
	Restriction Restriction
}

// IsAPIKey reports whether the caller authenticated with an API key.
func (p *Principal) IsAPIKey() bool { return p.APIKeyID != uuid.Nil }

// HasScope reports whether the principal holds want (sessions hold every scope).
func (p *Principal) HasScope(want Scope) bool {
	if !p.IsAPIKey() {
		return true
	}
	for _, s := range p.Scopes {
		if s.Rank() >= want.Rank() {
			return true
		}
	}
	return false
}

// Context returns ctx bound to the principal's workspace, as the store requires.
func (p *Principal) Context(ctx context.Context) context.Context {
	return store.WithWorkspace(ctx, p.WorkspaceID)
}

// CSRFToken returns the token a session must echo in X-CSRF-Token. It is bound to the session, so
// a token from one session is useless for another.
func (s *Service) CSRFToken(sessionID uuid.UUID) string {
	mac := hmac.New(sha256.New, s.csrfKey)
	mac.Write(sessionID[:])
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCSRF checks the header value for a session principal in constant time.
func (s *Service) VerifyCSRF(p *Principal, header string) bool {
	if p == nil || p.SessionID == uuid.Nil || header == "" {
		return false
	}
	return hmac.Equal([]byte(header), []byte(s.CSRFToken(p.SessionID)))
}

// Domain errors of this package.
var (
	ErrUnauthenticated    = apperr.New(apperr.KindUnauthenticated, "auth.unauthenticated")
	ErrInvalidCredentials = apperr.New(apperr.KindUnauthenticated, "auth.invalid_credentials")
	ErrMFAInvalid         = apperr.New(apperr.KindUnauthenticated, "auth.mfa_invalid")
	ErrChallengeExpired   = apperr.New(apperr.KindUnauthenticated, "auth.challenge_expired")
	ErrForbidden          = apperr.New(apperr.KindForbidden, "auth.forbidden")
	ErrSetupCompleted     = apperr.New(apperr.KindConflict, "setup.completed")
	ErrSetupInvalidToken  = apperr.New(apperr.KindForbidden, "setup.invalid_token")
	ErrLastAdmin          = apperr.New(apperr.KindConflict, "user.last_admin")
	ErrSelfModification   = apperr.New(apperr.KindConflict, "user.self_modification")
	ErrEmailTaken         = apperr.New(apperr.KindConflict, "user.email_taken")
	ErrTOTPAlreadyEnabled = apperr.New(apperr.KindConflict, "totp.already_enabled")
	ErrTOTPNotEnabled     = apperr.New(apperr.KindConflict, "totp.not_enabled")
	ErrTOTPNotStarted     = apperr.New(apperr.KindConflict, "totp.not_started")
)

func lockedError(retryAfter time.Duration) error {
	return &apperr.Error{Kind: apperr.KindTooManyRequests, Code: "auth.locked", RetryAfter: retryAfter}
}

// Security event types (docs/spec/07-security.md, "Security events").
const (
	EventSetupCompleted       = "setup_completed"
	EventLoginSuccess         = "login_success"
	EventLoginFailed          = "login_failed"
	EventLoginLocked          = "login_locked"
	EventLogout               = "logout"
	EventPasswordChanged      = "password_changed"
	EventPasswordReset        = "password_reset"
	Event2FAEnabled           = "2fa_enabled"
	Event2FADisabled          = "2fa_disabled"
	EventRecoveryCodesRenewed = "recovery_codes_regenerated"
	EventRecoveryCodeUsed     = "recovery_code_used"
	EventSessionRevoked       = "session_revoked"
	EventUserCreated          = "user_created"
	EventUserUpdated          = "user_updated"
	EventUserRoleChanged      = "user_role_changed"
	EventUserDisabled         = "user_disabled"
	EventUserEnabled          = "user_enabled"
	EventAPIKeyCreated        = "api_key_created"
	EventAPIKeyRevoked        = "api_key_revoked"
	EventSettingsChanged      = "settings_changed"
)

// record stores a security event in the workspace bound to ctx. A failure to record is logged and
// does not fail the action, except inside a transaction where the caller returns the error.
func (s *Service) record(ctx context.Context, actor *uuid.UUID, typ, ip string, meta map[string]any) error {
	err := s.store.SecurityEvents().Record(ctx, &store.SecurityEvent{ActorUserID: actor, Type: typ, IP: ip, Meta: meta})
	if err != nil {
		s.logger.WarnContext(ctx, "could not record security event", "type", typ, "error", err)
	}
	return err
}

func ptr[T any](v T) *T { return &v }
