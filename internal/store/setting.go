package store

import (
	"context"
	"encoding/json"

	"github.com/uptrace/bun"

	"github.com/rowbird/rowbird/internal/store/ids"
)

// Setting is one workspace setting (docs/spec/02-data-model.md). Value is JSON text. Secret values
// are stored encrypted by the caller and never returned by the API.
type Setting struct {
	bun.BaseModel `bun:"table:settings,alias:st"`
	TenantBase
	Key    string `bun:"key,notnull"`
	Value  string `bun:"value,notnull"`
	Secret bool   `bun:"secret,notnull"`
}

// Settings of system alerts and the outbound heartbeat (docs/spec/02-data-model.md). Other
// packages read the channel ids to show and protect the channels in use.
const (
	SettingAlertPrimaryChannel  = "system_alert_primary_channel_id"
	SettingAlertPrimaryOptions  = "system_alert_primary_options"
	SettingAlertFallbackChannel = "system_alert_fallback_channel_id"
	SettingAlertFallbackOptions = "system_alert_fallback_options"
	SettingHeartbeatURL         = "heartbeat_url"
	SettingHeartbeatInterval    = "heartbeat_interval"
)

// SettingUpdateCheck turns the daily update check off for the instance when false in any workspace.
const SettingUpdateCheck = "update_check"

// Retention settings and their defaults, in days (docs/spec/02-data-model.md, "Retention").
const (
	SettingRetentionRunsDays      = "retention_runs_days"
	SettingRetentionArtifactsDays = "retention_artifacts_days"
	DefaultRetentionRunsDays      = 90
	DefaultRetentionArtifactsDays = 30
)

// SettingRepo persists settings, scoped to the workspace in ctx.
type SettingRepo struct{ s *Store }

// Settings returns the settings repository.
func (s *Store) Settings() *SettingRepo { return &SettingRepo{s: s} }

// List returns every setting of the workspace.
func (r *SettingRepo) List(ctx context.Context) ([]Setting, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Setting
	err = sc.NewSelect(&out).OrderExpr("st.key").Scan(ctx)
	return out, mapError(err)
}

// Get returns one setting.
func (r *SettingRepo) Get(ctx context.Context, key string) (*Setting, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	st := new(Setting)
	err = sc.NewSelect(st).Where("st.key = ?", key).Scan(ctx)
	return st, mapError(err)
}

// Int reads a positive integer setting, def when it is missing or not a positive integer.
func (r *SettingRepo) Int(ctx context.Context, key string, def int) int {
	st, err := r.Get(ctx, key)
	if err != nil {
		return def
	}
	var n int
	if json.Unmarshal([]byte(st.Value), &n) != nil || n <= 0 {
		return def
	}
	return n
}

// Put inserts or replaces a setting. valueJSON must be valid JSON.
func (r *SettingRepo) Put(ctx context.Context, key, valueJSON string, secret bool) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	t := now()
	st := &Setting{Key: key, Value: valueJSON, Secret: secret}
	st.ID, st.CreatedAt, st.UpdatedAt, st.Version = ids.New(), t, t, 1
	st.WorkspaceID = sc.WorkspaceID()
	_, err = sc.db.NewInsert().Model(st).
		On("CONFLICT (workspace_id, key) DO UPDATE").
		Set("value = EXCLUDED.value").
		Set("secret = EXCLUDED.secret").
		Set("updated_at = EXCLUDED.updated_at").
		Set("version = st.version + 1").
		Exec(ctx)
	return mapError(err)
}

// Delete removes a setting; a missing one is not an error.
func (r *SettingRepo) Delete(ctx context.Context, key string) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	_, err = sc.NewDelete((*Setting)(nil)).Where("st.key = ?", key).Exec(ctx)
	return mapError(err)
}
