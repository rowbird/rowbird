package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/store"
)

// Passkeys (docs/spec/07-security.md, "Authentication"): WebAuthn credentials that sign a user in
// without a password, or act as the second factor after one. Either way they count as 2FA. The
// relying party is the host of the public base URL, so passkeys need ROWBIRD_BASE_URL.

// Passkey security events.
const (
	EventPasskeyAdded   = "passkey_added"
	EventPasskeyRemoved = "passkey_removed"
)

// Ceremony purposes stored with a WebAuthn challenge.
const (
	purposeRegister = "register"
	purposeLogin    = "login"
	purposeMFA      = "mfa"
)

// passkeyTTL bounds a ceremony; browsers give up well before.
const passkeyTTL = 5 * time.Minute

// MaxPasskeyName bounds the name a user gives a passkey.
const MaxPasskeyName = 100

// Passkey errors.
var (
	ErrPasskeysUnavailable = apperr.New(apperr.KindConflict, "passkey.unavailable")
	ErrPasskeyFailed       = apperr.New(apperr.KindInvalid, "passkey.failed")
	ErrPasskeyExists       = apperr.New(apperr.KindConflict, "passkey.exists")
)

// newWebAuthn builds the relying party from the base URL; nil when passkeys are unavailable.
func newWebAuthn(baseURL string) *webauthn.WebAuthn {
	u, err := url.Parse(baseURL)
	if baseURL == "" || err != nil || u.Hostname() == "" {
		return nil
	}
	w, err := webauthn.New(&webauthn.Config{
		RPID: u.Hostname(), RPDisplayName: "Rowbird", RPOrigins: []string{u.Scheme + "://" + u.Host},
	})
	if err != nil {
		return nil
	}
	return w
}

// PasskeysAvailable reports whether passkeys can be used on this instance.
func (s *Service) PasskeysAvailable() bool { return s.webauthn != nil }

// passkeyUser adapts a user and their passkeys to the WebAuthn library.
type passkeyUser struct {
	user  *store.User
	keys  []store.Passkey
	creds []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.user.ID[:] }
func (u *passkeyUser) WebAuthnName() string                       { return u.user.Email }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.user.Name }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (s *Service) loadPasskeyUser(ctx context.Context, user *store.User) (*passkeyUser, error) {
	keys, err := s.store.Passkeys().ListByUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	pu := &passkeyUser{user: user, keys: keys}
	for _, k := range keys {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(k.Credential), &c); err != nil {
			return nil, fmt.Errorf("auth: passkey %s: %w", k.ID, err)
		}
		pu.creds = append(pu.creds, c)
	}
	return pu, nil
}

func (pu *passkeyUser) stored(credentialID []byte) *store.Passkey {
	id := base64.RawURLEncoding.EncodeToString(credentialID)
	for i := range pu.keys {
		if pu.keys[i].CredentialID == id {
			return &pu.keys[i]
		}
	}
	return nil
}

// hasSecondFactor reports whether the user has TOTP or at least one passkey.
func (s *Service) hasSecondFactor(ctx context.Context, user *store.User) (bool, error) {
	if user.TOTPEnabled {
		return true, nil
	}
	n, err := s.store.Passkeys().CountByUser(ctx, user.ID)
	return n > 0, err
}

// secondFactorMethods lists what a user can finish a password login with.
func (s *Service) secondFactorMethods(ctx context.Context, user *store.User) ([]string, error) {
	var out []string
	if user.TOTPEnabled {
		out = append(out, "totp")
	}
	n, err := s.store.Passkeys().CountByUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if n > 0 && s.webauthn != nil {
		out = append(out, "passkey")
	}
	return out, nil
}

// PasskeyCeremony is what the browser needs to run navigator.credentials: the options and the
// token that ties its answer to the stored challenge.
type PasskeyCeremony struct {
	Token   string
	Options json.RawMessage
}

func (s *Service) saveCeremony(ctx context.Context, tokenHash, purpose string, userID *uuid.UUID, session *webauthn.SessionData) error {
	b, err := json.Marshal(session)
	if err != nil {
		return err
	}
	return s.store.Passkeys().PutChallenge(ctx, &store.WebAuthnChallenge{
		TokenHash: tokenHash, Purpose: purpose, UserID: userID, Data: string(b), ExpiresAt: s.clock().Add(passkeyTTL),
	})
}

func (s *Service) takeCeremony(ctx context.Context, tokenHash, purpose string) (*store.WebAuthnChallenge, *webauthn.SessionData, error) {
	c, err := s.store.Passkeys().TakeChallenge(ctx, tokenHash, purpose, s.clock())
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrChallengeExpired
	}
	if err != nil {
		return nil, nil, err
	}
	var session webauthn.SessionData
	if err := json.Unmarshal([]byte(c.Data), &session); err != nil {
		return nil, nil, err
	}
	return c, &session, nil
}

// BeginPasskeyRegistration starts adding a passkey to the caller's account. Like other changes to
// the second factor it requires the current password (users without one, signed in through OIDC,
// skip it).
func (s *Service) BeginPasskeyRegistration(ctx context.Context, p *Principal, password string) (*PasskeyCeremony, error) {
	if s.webauthn == nil {
		return nil, ErrPasskeysUnavailable
	}
	u, err := s.store.Users().Get(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	if u.PasswordHash != nil {
		if err := s.checkPassword(ctx, u, password, "password"); err != nil {
			return nil, err
		}
	}
	pu, err := s.loadPasskeyUser(ctx, u)
	if err != nil {
		return nil, err
	}
	exclude := make([]protocol.CredentialDescriptor, len(pu.creds))
	for i := range pu.creds {
		exclude[i] = pu.creds[i].Descriptor()
	}
	creation, session, err := s.webauthn.BeginRegistration(pu,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification: protocol.VerificationPreferred,
		}),
		webauthn.WithExclusions(exclude),
	)
	if err != nil {
		return nil, err
	}
	token, hash := NewSessionToken()
	if err := s.saveCeremony(ctx, hash, purposeRegister, &u.ID, session); err != nil {
		return nil, err
	}
	opts, err := json.Marshal(creation.Response)
	if err != nil {
		return nil, err
	}
	return &PasskeyCeremony{Token: token, Options: opts}, nil
}

// FinishPasskeyRegistration verifies the browser's answer and stores the passkey.
func (s *Service) FinishPasskeyRegistration(ctx context.Context, p *Principal, token, name string, credential json.RawMessage, meta RequestMeta) (*store.Passkey, error) {
	if s.webauthn == nil {
		return nil, ErrPasskeysUnavailable
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	if len([]rune(name)) > MaxPasskeyName {
		fe := fieldErrors{}
		fe.add("name", "validation.too_long")
		return nil, fe.err()
	}
	c, session, err := s.takeCeremony(ctx, HashToken(token), purposeRegister)
	if err != nil {
		return nil, err
	}
	if c.UserID == nil || *c.UserID != p.UserID {
		return nil, ErrChallengeExpired
	}
	u, err := s.store.Users().Get(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	pu, err := s.loadPasskeyUser(ctx, u)
	if err != nil {
		return nil, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(credential)
	if err != nil {
		return nil, ErrPasskeyFailed
	}
	cred, err := s.webauthn.CreateCredential(pu, *session, parsed)
	if err != nil {
		s.logger.InfoContext(ctx, "passkey registration refused", "user_id", u.ID.String(), "error", err)
		return nil, ErrPasskeyFailed
	}
	record, err := json.Marshal(cred)
	if err != nil {
		return nil, err
	}
	pk := &store.Passkey{
		UserID: u.ID, Name: name, CredentialID: base64.RawURLEncoding.EncodeToString(cred.ID),
		Credential: string(record), SignCount: int64(cred.Authenticator.SignCount),
	}
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Passkeys().Create(ctx, pk); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return ErrPasskeyExists
			}
			return err
		}
		return s.record(p.Context(ctx), &u.ID, EventPasskeyAdded, meta.IP, map[string]any{"name": name})
	})
	if err != nil {
		return nil, err
	}
	return pk, nil
}

// ListPasskeys returns the caller's passkeys.
func (s *Service) ListPasskeys(ctx context.Context, p *Principal) ([]store.Passkey, error) {
	return s.store.Passkeys().ListByUser(ctx, p.UserID)
}

// RenamePasskey changes the name of one of the caller's passkeys.
func (s *Service) RenamePasskey(ctx context.Context, p *Principal, id uuid.UUID, name string) (*store.Passkey, error) {
	name = strings.TrimSpace(name)
	fe := fieldErrors{}
	switch {
	case name == "":
		fe.add("name", CodeRequired)
	case len([]rune(name)) > MaxPasskeyName:
		fe.add("name", "validation.too_long")
	}
	if err := fe.err(); err != nil {
		return nil, err
	}
	pk, err := s.store.Passkeys().Get(ctx, p.UserID, id)
	if err != nil {
		return nil, err
	}
	pk.Name = name
	if err := s.store.Passkeys().Rename(ctx, pk); err != nil {
		return nil, err
	}
	return pk, nil
}

// RemovePasskey deletes one of the caller's passkeys after checking the password.
func (s *Service) RemovePasskey(ctx context.Context, p *Principal, id uuid.UUID, password string, meta RequestMeta) error {
	u, err := s.store.Users().Get(ctx, p.UserID)
	if err != nil {
		return err
	}
	pk, err := s.store.Passkeys().Get(ctx, p.UserID, id)
	if err != nil {
		return err
	}
	if u.PasswordHash != nil {
		if err := s.checkPassword(ctx, u, password, "password"); err != nil {
			return err
		}
	}
	return s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Passkeys().Delete(ctx, p.UserID, id); err != nil {
			return err
		}
		return s.record(p.Context(ctx), &u.ID, EventPasskeyRemoved, meta.IP, map[string]any{"name": pk.Name, "by": "self"})
	})
}

// BeginPasskeyLogin starts a sign-in with a passkey and no password: the browser offers the
// passkeys it has for this site, and the answer names the user.
func (s *Service) BeginPasskeyLogin(ctx context.Context) (*PasskeyCeremony, error) {
	if s.webauthn == nil {
		return nil, ErrPasskeysUnavailable
	}
	assertion, session, err := s.webauthn.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, err
	}
	token, hash := NewSessionToken()
	if err := s.saveCeremony(ctx, hash, purposeLogin, nil, session); err != nil {
		return nil, err
	}
	opts, err := json.Marshal(assertion.Response)
	if err != nil {
		return nil, err
	}
	return &PasskeyCeremony{Token: token, Options: opts}, nil
}

// FinishPasskeyLogin verifies the answer and signs the user in. The passkey verified the user
// (PIN or biometrics), so it satisfies the workspace's 2FA requirement on its own.
func (s *Service) FinishPasskeyLogin(ctx context.Context, token string, credential json.RawMessage, meta RequestMeta) (*IssuedSession, error) {
	if s.webauthn == nil {
		return nil, ErrPasskeysUnavailable
	}
	_, session, err := s.takeCeremony(ctx, HashToken(token), purposeLogin)
	if err != nil {
		return nil, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(credential)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	var pu *passkeyUser
	handler := func(_, userHandle []byte) (webauthn.User, error) {
		id, err := uuid.FromBytes(userHandle)
		if err != nil {
			return nil, err
		}
		u, err := s.store.Users().Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if pu, err = s.loadPasskeyUser(ctx, u); err != nil {
			return nil, err
		}
		return pu, nil
	}
	cred, verr := s.webauthn.ValidateDiscoverableLogin(handler, *session, parsed)
	if pu == nil {
		s.recordAnonymous(ctx, EventLoginFailed, meta, map[string]any{"reason": "unknown_passkey"})
		return nil, ErrInvalidCredentials
	}
	user := pu.user
	wsCtx, err := s.userWorkspace(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if user.Disabled() {
		_ = s.record(wsCtx, &user.ID, EventLoginFailed, meta.IP, map[string]any{"reason": "disabled"})
		return nil, ErrInvalidCredentials
	}
	if user.LockedUntil != nil && s.clock().Before(*user.LockedUntil) {
		return nil, lockedError(user.LockedUntil.Sub(s.clock()))
	}
	if err := s.usePasskey(ctx, pu, cred, verr); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return nil, s.registerFailure(wsCtx, user, meta, "passkey")
		}
		return nil, err
	}
	return s.completeLogin(ctx, wsCtx, user, meta, "passkey")
}

// usePasskey checks the outcome of an assertion and records the use. A signature counter that
// went backwards means the passkey may have been cloned, and the sign-in is refused.
func (s *Service) usePasskey(ctx context.Context, pu *passkeyUser, cred *webauthn.Credential, verr error) error {
	if verr != nil || cred == nil {
		return ErrInvalidCredentials
	}
	pk := pu.stored(cred.ID)
	if pk == nil {
		return ErrInvalidCredentials
	}
	if cred.Authenticator.CloneWarning {
		s.logger.WarnContext(ctx, "passkey signature counter went backwards; refusing it", "user_id", pu.user.ID.String(), "passkey_id", pk.ID.String())
		return ErrInvalidCredentials
	}
	record, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	return s.store.Passkeys().RecordUse(ctx, pk.ID, string(record), int64(cred.Authenticator.SignCount), s.clock())
}

// BeginPasskeySecondFactor starts the passkey step of a password login.
func (s *Service) BeginPasskeySecondFactor(ctx context.Context, challengeToken string) (json.RawMessage, error) {
	if s.webauthn == nil {
		return nil, ErrPasskeysUnavailable
	}
	c, err := s.store.Auth().ChallengeByTokenHash(ctx, HashToken(challengeToken))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrChallengeExpired
	}
	if err != nil {
		return nil, err
	}
	if !s.clock().Before(c.ExpiresAt) {
		return nil, ErrChallengeExpired
	}
	u, err := s.store.Users().Get(ctx, c.UserID)
	if err != nil {
		return nil, err
	}
	pu, err := s.loadPasskeyUser(ctx, u)
	if err != nil {
		return nil, err
	}
	if len(pu.creds) == 0 {
		return nil, ErrChallengeExpired
	}
	assertion, session, err := s.webauthn.BeginLogin(pu, webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		return nil, err
	}
	if err := s.saveCeremony(ctx, c.TokenHash, purposeMFA, &u.ID, session); err != nil {
		return nil, err
	}
	return json.Marshal(assertion.Response)
}

// checkPasskeySecondFactor verifies the passkey answer of a password login.
func (s *Service) checkPasskeySecondFactor(ctx context.Context, user *store.User, challengeHash string, credential json.RawMessage) (bool, error) {
	if s.webauthn == nil {
		return false, ErrPasskeysUnavailable
	}
	c, session, err := s.takeCeremony(ctx, challengeHash, purposeMFA)
	if errors.Is(err, ErrChallengeExpired) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if c.UserID == nil || *c.UserID != user.ID {
		return false, nil
	}
	parsed, perr := protocol.ParseCredentialRequestResponseBytes(credential)
	if perr != nil {
		// A malformed answer is a failed attempt, not a server error.
		return false, nil //nolint:nilerr // counted as a wrong second factor
	}
	pu, err := s.loadPasskeyUser(ctx, user)
	if err != nil {
		return false, err
	}
	cred, verr := s.webauthn.ValidateLogin(pu, *session, parsed)
	if err := s.usePasskey(ctx, pu, cred, verr); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// PasskeySynced reports whether a passkey can be synced between devices (backup eligible).
func PasskeySynced(pk *store.Passkey) bool {
	var c struct {
		Flags struct {
			BackupEligible bool `json:"backupEligible"`
		} `json:"flags"`
	}
	return json.Unmarshal([]byte(pk.Credential), &c) == nil && c.Flags.BackupEligible
}
