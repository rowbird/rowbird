package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rowbird/rowbird/internal/store"
)

// Setting keys owned by this package (docs/spec/02-data-model.md, "Setting").
const (
	SettingDefaultLocale   = "default_locale"
	SettingDefaultTimezone = "default_timezone"
	SettingRequire2FA      = "require_2fa"
)

// Settings is the typed view of the workspace settings managed so far.
type Settings struct {
	DefaultLocale          string
	DefaultTimezone        string
	Require2FA             bool
	RetentionRunsDays      int
	RetentionArtifactsDays int
	UpdateCheck            bool
}

var defaultSettings = Settings{
	DefaultLocale: "en", DefaultTimezone: "UTC", UpdateCheck: true,
	RetentionRunsDays: store.DefaultRetentionRunsDays, RetentionArtifactsDays: store.DefaultRetentionArtifactsDays,
}

// MaxRetentionDays bounds the retention settings (ten years).
const MaxRetentionDays = 3650

// GetSettings reads the workspace settings, filling defaults for missing keys.
func (s *Service) GetSettings(ctx context.Context) (Settings, error) {
	rows, err := s.store.Settings().List(ctx)
	if err != nil {
		return Settings{}, err
	}
	out := defaultSettings
	for _, r := range rows {
		var target any
		switch r.Key {
		case SettingDefaultLocale:
			target = &out.DefaultLocale
		case SettingDefaultTimezone:
			target = &out.DefaultTimezone
		case SettingRequire2FA:
			target = &out.Require2FA
		case store.SettingRetentionRunsDays:
			target = &out.RetentionRunsDays
		case store.SettingRetentionArtifactsDays:
			target = &out.RetentionArtifactsDays
		case store.SettingUpdateCheck:
			target = &out.UpdateCheck
		default:
			continue
		}
		if err := json.Unmarshal([]byte(r.Value), target); err != nil {
			return Settings{}, fmt.Errorf("auth: setting %s is not valid JSON: %w", r.Key, err)
		}
	}
	return out, nil
}

// require2FA reports whether the workspace requires every user to have a second factor.
func (s *Service) require2FA(ctx context.Context) (bool, error) {
	st, err := s.store.Settings().Get(ctx, SettingRequire2FA)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var v bool
	return v, json.Unmarshal([]byte(st.Value), &v)
}

// SettingsPatch changes the fields that are not nil.
type SettingsPatch struct {
	DefaultLocale          *string
	DefaultTimezone        *string
	Require2FA             *bool
	RetentionRunsDays      *int
	RetentionArtifactsDays *int
	UpdateCheck            *bool
}

// UpdateSettings applies a patch (admin only, enforced by the API) and records what changed.
func (s *Service) UpdateSettings(ctx context.Context, p *Principal, patch SettingsPatch, meta RequestMeta) (Settings, error) {
	var fe fieldErrors
	if patch.DefaultLocale != nil {
		fe.locale("default_locale", *patch.DefaultLocale)
	}
	if patch.DefaultTimezone != nil {
		fe.timezone("default_timezone", *patch.DefaultTimezone)
	}
	for field, v := range map[string]*int{"retention_runs_days": patch.RetentionRunsDays, "retention_artifacts_days": patch.RetentionArtifactsDays} {
		if v != nil && (*v < 1 || *v > MaxRetentionDays) {
			fe.add(field, "validation.range")
		}
	}
	if err := fe.err(); err != nil {
		return Settings{}, err
	}

	changed := map[string]any{}
	err := s.store.RunInTx(ctx, func(ctx context.Context) error {
		put := func(key string, v any) error {
			b, _ := json.Marshal(v)
			changed[key] = v
			return s.store.Settings().Put(ctx, key, string(b), false)
		}
		if patch.DefaultLocale != nil {
			if err := put(SettingDefaultLocale, *patch.DefaultLocale); err != nil {
				return err
			}
		}
		if patch.DefaultTimezone != nil {
			if err := put(SettingDefaultTimezone, *patch.DefaultTimezone); err != nil {
				return err
			}
		}
		if patch.Require2FA != nil {
			if err := put(SettingRequire2FA, *patch.Require2FA); err != nil {
				return err
			}
		}
		if patch.RetentionRunsDays != nil {
			if err := put(store.SettingRetentionRunsDays, *patch.RetentionRunsDays); err != nil {
				return err
			}
		}
		if patch.RetentionArtifactsDays != nil {
			if err := put(store.SettingRetentionArtifactsDays, *patch.RetentionArtifactsDays); err != nil {
				return err
			}
		}
		if patch.UpdateCheck != nil {
			if err := put(store.SettingUpdateCheck, *patch.UpdateCheck); err != nil {
				return err
			}
		}
		if len(changed) == 0 {
			return nil
		}
		return s.record(ctx, &p.UserID, EventSettingsChanged, meta.IP, changed)
	})
	if err != nil {
		return Settings{}, err
	}
	return s.GetSettings(ctx)
}
