package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/store"
)

// Me returns the caller's user record.
func (s *Service) Me(ctx context.Context, p *Principal) (*store.User, error) {
	return s.store.Users().Get(ctx, p.UserID)
}

// ProfilePatch changes the fields that are not nil. Version must match the stored user.
type ProfilePatch struct {
	Version int64
	Name    *string
	Locale  *string
	Theme   *string
}

// UpdateProfile saves the caller's own name and preferences.
func (s *Service) UpdateProfile(ctx context.Context, p *Principal, patch ProfilePatch) (*store.User, error) {
	u, err := s.store.Users().Get(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	u.Version = patch.Version
	var fe fieldErrors
	var cols []string
	if patch.Name != nil {
		u.Name = fe.name("name", *patch.Name)
		cols = append(cols, "name")
	}
	if patch.Locale != nil {
		fe.locale("locale", *patch.Locale)
		u.Locale = *patch.Locale
		cols = append(cols, "locale")
	}
	if patch.Theme != nil {
		fe.theme("theme", *patch.Theme)
		u.Theme = *patch.Theme
		cols = append(cols, "theme")
	}
	if err := fe.err(); err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return s.store.Users().Get(ctx, p.UserID)
	}
	if err := s.store.Users().Update(ctx, u, cols...); err != nil {
		return nil, err
	}
	return u, nil
}

// ChangePassword replaces the caller's password after checking the current one. Other sessions are
// revoked; the current one stays signed in and loses the password-change restriction.
func (s *Service) ChangePassword(ctx context.Context, p *Principal, current, next string, meta RequestMeta) error {
	u, err := s.store.Users().Get(ctx, p.UserID)
	if err != nil {
		return err
	}
	if err := s.checkPassword(ctx, u, current, "current_password"); err != nil {
		return err
	}
	var fe fieldErrors
	fe.password("new_password", next, u.Email)
	if current == next {
		fe.add("new_password", CodeInvalidValue)
	}
	if err := fe.err(); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, next)
	if err != nil {
		return err
	}
	u.PasswordHash = &hash
	u.MustChangePassword = false
	return s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Users().Update(ctx, u, "password_hash", "must_change_password"); err != nil {
			return err
		}
		if _, err := s.store.Auth().RevokeUserSessions(ctx, u.ID, p.SessionID, s.clock()); err != nil {
			return err
		}
		return s.record(p.Context(ctx), &u.ID, EventPasswordChanged, meta.IP, nil)
	})
}

// checkPassword re-authenticates the caller for sensitive changes. A wrong password is a field
// error, not an authentication failure, so the session is not affected.
func (s *Service) checkPassword(ctx context.Context, u *store.User, password, field string) error {
	if u.PasswordHash == nil {
		return (&fieldErrors{{Field: field, Code: CodeInvalidValue}}).err()
	}
	ok, _, err := s.hasher.Verify(ctx, password, *u.PasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		fe := fieldErrors{}
		fe.add(field, "validation.password_incorrect")
		return fe.err()
	}
	return nil
}

// ListSessions returns the caller's active sessions.
func (s *Service) ListSessions(ctx context.Context, p *Principal) ([]store.Session, error) {
	return s.store.Auth().ListActiveSessions(ctx, p.UserID, s.clock())
}

// RevokeSession signs out one of the caller's sessions.
func (s *Service) RevokeSession(ctx context.Context, p *Principal, id uuid.UUID, meta RequestMeta) error {
	if err := s.store.Auth().RevokeSession(ctx, p.UserID, id, s.clock()); err != nil {
		return err
	}
	_ = s.record(p.Context(ctx), &p.UserID, EventSessionRevoked, meta.IP, map[string]any{"session_id": id.String()})
	return nil
}

// RevokeAllSessions signs the caller out everywhere, including the current session.
func (s *Service) RevokeAllSessions(ctx context.Context, p *Principal, meta RequestMeta) error {
	n, err := s.store.Auth().RevokeUserSessions(ctx, p.UserID, uuid.Nil, s.clock())
	if err != nil {
		return err
	}
	_ = s.record(p.Context(ctx), &p.UserID, EventSessionRevoked, meta.IP, map[string]any{"count": n, "all": true})
	return nil
}

// BeginTOTP generates a new pending secret. It becomes active only after ConfirmTOTP.
func (s *Service) BeginTOTP(ctx context.Context, p *Principal) (*TOTPEnrollment, error) {
	u, err := s.store.Users().Get(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	if u.TOTPEnabled {
		return nil, ErrTOTPAlreadyEnabled
	}
	e, err := NewTOTPEnrollment(s.cfg.TOTPIssuer, u.Email)
	if err != nil {
		return nil, err
	}
	enc, err := s.keyring.Encrypt([]byte(e.Secret), TOTPAssociatedData(u.ID))
	if err != nil {
		return nil, err
	}
	u.TOTPSecretEnc = &enc
	if err := s.store.Users().Update(ctx, u, "totp_secret_enc"); err != nil {
		return nil, err
	}
	return e, nil
}

// ConfirmTOTP activates the pending secret with a valid code and returns fresh recovery codes,
// which are shown once.
func (s *Service) ConfirmTOTP(ctx context.Context, p *Principal, code string, meta RequestMeta) ([]string, error) {
	u, err := s.store.Users().Get(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	if u.TOTPEnabled {
		return nil, ErrTOTPAlreadyEnabled
	}
	if u.TOTPSecretEnc == nil {
		return nil, ErrTOTPNotStarted
	}
	secret, err := s.totpSecret(u)
	if err != nil {
		return nil, err
	}
	step, ok := MatchTOTP(secret, code, s.clock())
	if !ok {
		return nil, invalidCode()
	}
	codes, hashes := NewRecoveryCodes()
	u.TOTPEnabled, u.TOTPLastStep, u.RecoveryCodes = true, step, hashes
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Users().Update(ctx, u, "totp_enabled", "totp_last_step", "recovery_codes"); err != nil {
			return err
		}
		return s.record(p.Context(ctx), &u.ID, Event2FAEnabled, meta.IP, nil)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// DisableTOTP removes the second factor after checking the password.
func (s *Service) DisableTOTP(ctx context.Context, p *Principal, password string, meta RequestMeta) error {
	u, err := s.store.Users().Get(ctx, p.UserID)
	if err != nil {
		return err
	}
	if !u.TOTPEnabled {
		return ErrTOTPNotEnabled
	}
	if err := s.checkPassword(ctx, u, password, "password"); err != nil {
		return err
	}
	return s.clearTOTP(p.Context(ctx), u, &p.UserID, meta, "self")
}

func (s *Service) clearTOTP(wsCtx context.Context, u *store.User, actor *uuid.UUID, meta RequestMeta, by string) error {
	u.TOTPEnabled, u.TOTPSecretEnc, u.RecoveryCodes = false, nil, nil
	return s.store.RunInTx(wsCtx, func(ctx context.Context) error {
		if err := s.store.Users().Update(ctx, u, "totp_enabled", "totp_secret_enc", "recovery_codes"); err != nil {
			return err
		}
		return s.record(ctx, actor, Event2FADisabled, meta.IP, map[string]any{"user_id": u.ID.String(), "by": by})
	})
}

// RegenerateRecoveryCodes replaces the recovery codes after checking the password.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, p *Principal, password string, meta RequestMeta) ([]string, error) {
	u, err := s.store.Users().Get(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	if !u.TOTPEnabled {
		return nil, ErrTOTPNotEnabled
	}
	if err := s.checkPassword(ctx, u, password, "password"); err != nil {
		return nil, err
	}
	codes, hashes := NewRecoveryCodes()
	u.RecoveryCodes = hashes
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Users().Update(ctx, u, "recovery_codes"); err != nil {
			return err
		}
		return s.record(p.Context(ctx), &u.ID, EventRecoveryCodesRenewed, meta.IP, nil)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

func invalidCode() error {
	fe := fieldErrors{}
	fe.add("code", "validation.code_invalid")
	return fe.err()
}
