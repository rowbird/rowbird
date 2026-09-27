package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/store"
)

// UserWithRole is a user as seen from a workspace.
type UserWithRole struct {
	User store.User
	Role store.Role
}

// ListUsers lists the members of the caller's workspace.
func (s *Service) ListUsers(ctx context.Context, req store.PageRequest) (store.Page[UserWithRole], error) {
	page, err := s.store.Members().List(ctx, req)
	if err != nil {
		return store.Page[UserWithRole]{}, err
	}
	out := store.Page[UserWithRole]{NextCursor: page.NextCursor, Items: make([]UserWithRole, len(page.Items))}
	for i, mu := range page.Items {
		out.Items[i] = UserWithRole{User: mu.User, Role: mu.Member.Role}
	}
	return out, nil
}

// GetUser returns a member of the caller's workspace. Users outside it are not found.
func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (*UserWithRole, error) {
	m, err := s.store.Members().GetByUser(ctx, id)
	if err != nil {
		return nil, err
	}
	u, err := s.store.Users().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &UserWithRole{User: *u, Role: m.Role}, nil
}

// NewUserInput is what an admin provides to add a user.
type NewUserInput struct {
	Email  string
	Name   string
	Role   store.Role
	Locale string
	// Password is optional. When empty a temporary password is generated and the user must change
	// it at the first sign-in. A password chosen by the caller (the CLI, for automation) is kept.
	Password string
}

// CreateUser adds a user to the caller's workspace and returns the temporary password, which is
// shown once to the admin.
func (s *Service) CreateUser(ctx context.Context, p *Principal, in NewUserInput, meta RequestMeta) (*UserWithRole, string, error) {
	var fe fieldErrors
	email := fe.email("email", in.Email)
	name := fe.name("name", in.Name)
	fe.role("role", in.Role)
	if in.Locale == "" {
		settings, err := s.GetSettings(ctx)
		if err != nil {
			return nil, "", err
		}
		in.Locale = settings.DefaultLocale
	}
	fe.locale("locale", in.Locale)
	password, temporary := in.Password, in.Password == ""
	if temporary {
		password = NewTemporaryPassword()
	} else {
		fe.password("password", password, email)
	}
	if err := fe.err(); err != nil {
		return nil, "", err
	}
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return nil, "", err
	}

	u := &store.User{Email: email, Name: name, PasswordHash: &hash, Locale: in.Locale, Theme: "system", MustChangePassword: temporary}
	u.CreatedBy = actorPtr(p)
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Users().Create(ctx, u); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return ErrEmailTaken
			}
			return err
		}
		m := &store.Member{UserID: u.ID, Role: in.Role}
		m.CreatedBy = actorPtr(p)
		if err := s.store.Members().Add(ctx, m); err != nil {
			return err
		}
		return s.record(ctx, actorPtr(p), EventUserCreated, meta.IP, map[string]any{"user_id": u.ID.String(), "email": email, "role": string(in.Role)})
	})
	if err != nil {
		return nil, "", err
	}
	return &UserWithRole{User: *u, Role: in.Role}, password, nil
}

// UserPatch changes the fields that are not nil. Version is the user's version.
type UserPatch struct {
	Version  int64
	Name     *string
	Role     *store.Role
	Disabled *bool
}

// UpdateUser changes a member's name, role or enabled state. It refuses to leave the workspace
// without an active admin and to let admins disable or demote themselves. Role changes and
// disabling sign the user out everywhere; disabling also revokes their API keys.
func (s *Service) UpdateUser(ctx context.Context, p *Principal, id uuid.UUID, patch UserPatch, meta RequestMeta) (*UserWithRole, error) {
	var fe fieldErrors
	if patch.Name != nil {
		*patch.Name = fe.name("name", *patch.Name)
	}
	if patch.Role != nil {
		fe.role("role", *patch.Role)
	}
	if err := fe.err(); err != nil {
		return nil, err
	}

	var result *UserWithRole
	err := s.store.RunInTx(ctx, func(ctx context.Context) error {
		m, err := s.store.Members().GetByUser(ctx, id)
		if err != nil {
			return err
		}
		u, err := s.store.Users().Get(ctx, id)
		if err != nil {
			return err
		}
		u.Version = patch.Version
		now := s.clock()
		self := p != nil && p.UserID == id

		roleChanged := patch.Role != nil && *patch.Role != m.Role
		disabling := patch.Disabled != nil && *patch.Disabled && !u.Disabled()
		enabling := patch.Disabled != nil && !*patch.Disabled && u.Disabled()
		if self && (roleChanged || disabling) {
			return ErrSelfModification
		}
		if m.Role == store.RoleAdmin && !u.Disabled() && (roleChanged || disabling) {
			n, err := s.store.Members().CountActiveAdmins(ctx)
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastAdmin
			}
		}

		cols := []string{}
		if patch.Name != nil {
			u.Name = *patch.Name
			cols = append(cols, "name")
		}
		if disabling {
			u.DisabledAt = &now
			cols = append(cols, "disabled_at")
		}
		if enabling {
			u.DisabledAt = nil
			cols = append(cols, "disabled_at")
		}
		// The user row is always written so its version guards the whole change, role included.
		if err := s.store.Users().Update(ctx, u, cols...); err != nil {
			return err
		}
		if roleChanged {
			old := m.Role
			m.Role = *patch.Role
			if err := s.store.Members().Update(ctx, m); err != nil {
				return err
			}
			if err := s.record(ctx, actorPtr(p), EventUserRoleChanged, meta.IP, map[string]any{"user_id": id.String(), "from": string(old), "to": string(m.Role)}); err != nil {
				return err
			}
		}
		if roleChanged || disabling {
			if _, err := s.store.Auth().RevokeUserSessions(ctx, id, uuid.Nil, now); err != nil {
				return err
			}
		}
		if disabling {
			if err := s.store.APIKeys().RevokeAllOfUser(ctx, id, now); err != nil {
				return err
			}
		}
		switch {
		case disabling:
			err = s.record(ctx, actorPtr(p), EventUserDisabled, meta.IP, map[string]any{"user_id": id.String()})
		case enabling:
			err = s.record(ctx, actorPtr(p), EventUserEnabled, meta.IP, map[string]any{"user_id": id.String()})
		case patch.Name != nil:
			err = s.record(ctx, actorPtr(p), EventUserUpdated, meta.IP, map[string]any{"user_id": id.String()})
		}
		if err != nil {
			return err
		}
		result = &UserWithRole{User: *u, Role: m.Role}
		return nil
	})
	return result, err
}

// ResetPassword gives a user a new temporary password (returned once), signs them out everywhere
// and forces a change at the next sign-in. It also unlocks the account.
func (s *Service) ResetPassword(ctx context.Context, p *Principal, id uuid.UUID, meta RequestMeta) (string, error) {
	if _, err := s.store.Members().GetByUser(ctx, id); err != nil {
		return "", err
	}
	password := NewTemporaryPassword()
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return "", err
	}
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		u, err := s.store.Users().Get(ctx, id)
		if err != nil {
			return err
		}
		u.PasswordHash, u.MustChangePassword, u.FailedLogins, u.LockedUntil = &hash, true, 0, nil
		if err := s.store.Users().Update(ctx, u, "password_hash", "must_change_password", "failed_logins", "locked_until"); err != nil {
			return err
		}
		if _, err := s.store.Auth().RevokeUserSessions(ctx, id, uuid.Nil, s.clock()); err != nil {
			return err
		}
		return s.record(ctx, actorPtr(p), EventPasswordReset, meta.IP, map[string]any{"user_id": id.String()})
	})
	if err != nil {
		return "", err
	}
	return password, nil
}

// AdminDisableTOTP removes another user's second factor, for users who lost their device and codes.
func (s *Service) AdminDisableTOTP(ctx context.Context, p *Principal, id uuid.UUID, meta RequestMeta) error {
	if _, err := s.store.Members().GetByUser(ctx, id); err != nil {
		return err
	}
	u, err := s.store.Users().Get(ctx, id)
	if err != nil {
		return err
	}
	has2FA, err := s.hasSecondFactor(ctx, u)
	if err != nil {
		return err
	}
	if !has2FA {
		return ErrTOTPNotEnabled
	}
	// Someone who lost their phone may have lost their passkeys with it: both go.
	n, err := s.store.Passkeys().DeleteByUser(ctx, u.ID)
	if err != nil {
		return err
	}
	if n > 0 {
		_ = s.record(ctx, actorPtr(p), EventPasskeyRemoved, meta.IP, map[string]any{"user_id": u.ID.String(), "count": n, "by": "admin"})
	}
	if !u.TOTPEnabled {
		return nil
	}
	return s.clearTOTP(ctx, u, actorPtr(p), meta, "admin")
}

// SetRole changes a member's role without a version check. It exists for the CLI, which acts as an
// operator recovering access and has no user to attribute the change to.
func (s *Service) SetRole(ctx context.Context, id uuid.UUID, role store.Role, meta RequestMeta) error {
	u, err := s.store.Users().Get(ctx, id)
	if err != nil {
		return err
	}
	_, err = s.UpdateUser(ctx, nil, id, UserPatch{Version: u.Version, Role: &role}, meta)
	return err
}

// actorPtr returns the acting user's id, or nil for the CLI (no principal).
func actorPtr(p *Principal) *uuid.UUID {
	if p == nil {
		return nil
	}
	return ptr(p.UserID)
}
