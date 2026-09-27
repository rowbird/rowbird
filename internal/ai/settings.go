package ai

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/secretconfig"
	"github.com/rowbird/rowbird/internal/store"
)

// Settings keys. The provider's plain settings and its encrypted secrets are kept apart, like a
// channel's (ADR-0022); the secrets' associated data binds them to the workspace.
const (
	SettingProvider = "ai_provider"
	SettingConfig   = "ai_config"
	SettingSecrets  = "ai_secrets"
)

// SecretsKind is the associated-data kind of the encrypted AI secrets, bound to the workspace id.
const SecretsKind = "setting:ai"

// EventSettingsChanged is the security event of a change to the AI settings.
const EventSettingsChanged = "settings_changed"

// ErrUnknownProvider is a provider id that is not compiled in.
var ErrUnknownProvider = apperr.Invalid(apperr.Field("provider", "validation.invalid_value"))

// Settings are the AI settings of a workspace. Config holds plain values, and secrets as
// {"configured": true}.
type Settings struct {
	Provider string
	Config   map[string]any
}

// Enabled reports whether a provider is configured.
func (s Settings) Enabled() bool { return s.Provider != "" }

// SettingsInput replaces the settings; an empty provider turns the assistant off. Secret fields
// follow the placeholder rules when the provider stays the same.
type SettingsInput struct {
	Provider string
	Config   map[string]any
}

type stored struct {
	provider string
	config   map[string]any
	secrets  *string
}

func (s *Service) load(ctx context.Context) (*stored, error) {
	st, err := s.store.Settings().Get(ctx, SettingProvider)
	if errors.Is(err, store.ErrNotFound) {
		return &stored{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := &stored{config: map[string]any{}}
	if err := json.Unmarshal([]byte(st.Value), &out.provider); err != nil {
		return nil, err
	}
	if c, err := s.store.Settings().Get(ctx, SettingConfig); err == nil {
		_ = json.Unmarshal([]byte(c.Value), &out.config)
	}
	if c, err := s.store.Settings().Get(ctx, SettingSecrets); err == nil {
		var enc string
		if json.Unmarshal([]byte(c.Value), &enc) == nil {
			out.secrets = &enc
		}
	}
	return out, nil
}

func workspace(ctx context.Context) (uuid.UUID, error) {
	ws, ok := store.WorkspaceFrom(ctx)
	if !ok {
		return uuid.Nil, store.ErrNoWorkspace
	}
	return ws, nil
}

// GetSettings returns the settings with secrets as placeholders.
func (s *Service) GetSettings(ctx context.Context) (Settings, error) {
	st, err := s.load(ctx)
	if err != nil || st.provider == "" {
		return Settings{Config: map[string]any{}}, err
	}
	out := Settings{Provider: st.provider, Config: map[string]any{}}
	for k, v := range st.config {
		out.Config[k] = v
	}
	p, ok := s.provider(st.provider)
	if !ok {
		return out, nil
	}
	ws, err := workspace(ctx)
	if err != nil {
		return out, err
	}
	secrets, err := s.secrets.Secrets(ws, st.secrets)
	if err != nil {
		return out, err
	}
	configured := secretconfig.Configured(p.ConfigSchema(), secrets)
	for _, k := range p.ConfigSchema().SecretKeys() {
		out.Config[k] = map[string]any{"configured": configured[k]}
	}
	return out, nil
}

// resolve validates input against the provider's schema, taking stored secrets for placeholders
// when the provider is the one saved.
func (s *Service) resolve(ctx context.Context, in SettingsInput) (plugin.AIProvider, map[string]any, error) {
	p, ok := s.provider(in.Provider)
	if !ok {
		return nil, nil, ErrUnknownProvider
	}
	existing := map[string]any{}
	st, err := s.load(ctx)
	if err != nil {
		return nil, nil, err
	}
	if st.provider == in.Provider {
		ws, err := workspace(ctx)
		if err != nil {
			return nil, nil, err
		}
		if existing, err = s.secrets.Secrets(ws, st.secrets); err != nil {
			return nil, nil, err
		}
	}
	config := in.Config
	if config == nil {
		config = map[string]any{}
	}
	validated, err := p.ConfigSchema().Validate(secretconfig.Resolve(p.ConfigSchema(), config, existing))
	if err != nil {
		if ae, ok := apperr.As(err); ok {
			fe := make([]apperr.FieldError, len(ae.Fields))
			for i, f := range ae.Fields {
				fe[i] = apperr.Field("config."+f.Field, f.Code)
			}
			return nil, nil, apperr.Invalid(fe...)
		}
		return nil, nil, err
	}
	return p, validated, nil
}

// UpdateSettings replaces the settings (admin only, enforced by the API).
func (s *Service) UpdateSettings(ctx context.Context, pr *auth.Principal, in SettingsInput, meta auth.RequestMeta) (Settings, error) {
	ws, err := workspace(ctx)
	if err != nil {
		return Settings{}, err
	}
	var p plugin.AIProvider
	var validated map[string]any
	if in.Provider != "" {
		if p, validated, err = s.resolve(ctx, in); err != nil {
			return Settings{}, err
		}
	}
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		if p == nil {
			for _, k := range []string{SettingProvider, SettingConfig, SettingSecrets} {
				if err := s.store.Settings().Delete(ctx, k); err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
			}
		} else {
			cfg, enc, err := s.secrets.Seal(ws, p.ConfigSchema(), validated)
			if err != nil {
				return err
			}
			id, _ := json.Marshal(in.Provider)
			plain, _ := json.Marshal(cfg)
			if err := s.store.Settings().Put(ctx, SettingProvider, string(id), false); err != nil {
				return err
			}
			if err := s.store.Settings().Put(ctx, SettingConfig, string(plain), false); err != nil {
				return err
			}
			if enc == nil {
				if err := s.store.Settings().Delete(ctx, SettingSecrets); err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
			} else {
				b, _ := json.Marshal(*enc)
				if err := s.store.Settings().Put(ctx, SettingSecrets, string(b), true); err != nil {
					return err
				}
			}
		}
		return s.store.SecurityEvents().Record(ctx, &store.SecurityEvent{
			ActorUserID: &pr.UserID, Type: EventSettingsChanged, IP: meta.IP, Meta: map[string]any{"keys": []string{"ai"}, "provider": in.Provider},
		})
	})
	if err != nil {
		return Settings{}, err
	}
	s.logger.InfoContext(ctx, "ai settings changed", "provider", in.Provider)
	return s.GetSettings(ctx)
}

// TestResult is the outcome of a settings test.
type TestResult struct {
	OK           bool
	ErrorCode    string
	ErrorMessage string
}

// TestSettings checks settings before they are saved; placeholders take the stored secrets.
func (s *Service) TestSettings(ctx context.Context, in SettingsInput) (TestResult, error) {
	p, validated, err := s.resolve(ctx, in)
	if err != nil {
		return TestResult{}, err
	}
	env := s.env(validated)
	if err := p.Test(ctx, env); err != nil {
		code := plugin.ErrCodeAIFailed
		var ae *plugin.AIError
		if errors.As(err, &ae) {
			code = ae.Code
		}
		return TestResult{ErrorCode: code, ErrorMessage: secretconfig.Scrub(err.Error(), p.ConfigSchema(), env.Config)}, nil
	}
	return TestResult{OK: true}, nil
}

func (s *Service) env(values map[string]any) plugin.AIEnv {
	// Each request carries the provider's own timeout; this one only bounds a stuck connection.
	return plugin.AIEnv{Config: values, HTTP: netx.HTTPClient(s.dial, 15*time.Minute)}
}

// configured returns the provider and its environment, or ErrNotConfigured.
func (s *Service) configured(ctx context.Context) (plugin.AIProvider, plugin.AIEnv, error) {
	st, err := s.load(ctx)
	if err != nil {
		return nil, plugin.AIEnv{}, err
	}
	p, ok := s.provider(st.provider)
	if st.provider == "" || !ok {
		return nil, plugin.AIEnv{}, ErrNotConfigured
	}
	ws, err := workspace(ctx)
	if err != nil {
		return nil, plugin.AIEnv{}, err
	}
	values, err := s.secrets.Values(ws, p.ConfigSchema(), st.config, st.secrets)
	if err != nil {
		return nil, plugin.AIEnv{}, err
	}
	return p, s.env(values), nil
}
