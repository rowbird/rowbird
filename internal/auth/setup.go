package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"

	"github.com/rowbird/rowbird/internal/store"
)

// DefaultWorkspaceSlug names the single workspace of the OSS edition.
const DefaultWorkspaceSlug = "default"

// SetupStatus tells the wizard whether it should run.
type SetupStatus struct {
	Required      bool
	TokenRequired bool
}

// SetupStatus reports whether first-run setup is still pending.
func (s *Service) SetupStatus(ctx context.Context) (SetupStatus, error) {
	any, err := s.store.Users().Any(ctx)
	if err != nil {
		return SetupStatus{}, err
	}
	return SetupStatus{Required: !any, TokenRequired: s.cfg.SetupToken.IsSet()}, nil
}

// SetupInput is what the wizard collects (docs/spec/03-flows.md, section 1).
type SetupInput struct {
	Token    string
	Email    string
	Name     string
	Password string
	Locale   string
	Timezone string
}

// Setup creates the default workspace, the first admin and the initial settings, then signs the
// admin in. It fails forever once any user exists.
func (s *Service) Setup(ctx context.Context, in SetupInput, meta RequestMeta) (*IssuedSession, error) {
	if s.cfg.SetupToken.IsSet() && subtle.ConstantTimeCompare([]byte(in.Token), []byte(s.cfg.SetupToken.Reveal())) != 1 {
		return nil, ErrSetupInvalidToken
	}
	var fe fieldErrors
	email := fe.email("email", in.Email)
	name := fe.name("name", in.Name)
	fe.password("password", in.Password, email)
	fe.locale("locale", in.Locale)
	fe.timezone("timezone", in.Timezone)
	if err := fe.err(); err != nil {
		return nil, err
	}
	if st, err := s.SetupStatus(ctx); err != nil {
		return nil, err
	} else if !st.Required {
		return nil, ErrSetupCompleted
	}

	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return nil, err
	}

	var user *store.User
	var wsCtx context.Context
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		// Checked again inside the transaction; the unique workspace slug settles a race between
		// two wizards submitted at the same time.
		if any, err := s.store.Users().Any(ctx); err != nil {
			return err
		} else if any {
			return ErrSetupCompleted
		}
		ws := &store.Workspace{Name: "Default", Slug: DefaultWorkspaceSlug}
		if err := s.store.Workspaces().Create(ctx, ws); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return ErrSetupCompleted
			}
			return err
		}
		user = &store.User{Email: email, Name: name, PasswordHash: &hash, Locale: in.Locale, Theme: "system"}
		if err := s.store.Users().Create(ctx, user); err != nil {
			return err
		}
		wsCtx = store.WithWorkspace(ctx, ws.ID)
		if err := s.store.Members().Add(wsCtx, &store.Member{UserID: user.ID, Role: store.RoleAdmin}); err != nil {
			return err
		}
		for key, v := range map[string]any{
			SettingDefaultLocale:   in.Locale,
			SettingDefaultTimezone: in.Timezone,
			SettingRequire2FA:      false,
		} {
			b, _ := json.Marshal(v)
			if err := s.store.Settings().Put(wsCtx, key, string(b), false); err != nil {
				return err
			}
		}
		return s.record(wsCtx, &user.ID, EventSetupCompleted, meta.IP, map[string]any{"email": email})
	})
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return nil, ErrSetupCompleted
		}
		return nil, err
	}
	s.logger.InfoContext(ctx, "setup completed", "user_id", user.ID.String())
	return s.startSession(ctx, user, meta, SessionPassword)
}
