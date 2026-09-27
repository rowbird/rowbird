package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// Kinds of encrypted values, as listed by SecretValueRepo.
const (
	SecretUserTOTP   = "user_totp"
	SecretConnection = "connection"
	SecretChannel    = "channel"
	SecretSetting    = "setting"
)

// SecretValue is one encrypted value stored in the database. Setting values are unwrapped from
// their JSON string, so Value is always the ciphertext itself.
type SecretValue struct {
	Kind        string
	ID          uuid.UUID
	WorkspaceID uuid.UUID // zero for users, which are not tenant-owned
	Key         string    // the setting key, for settings
	Value       string
}

// SecretValueRepo lists and replaces every encrypted value across workspaces. It exists for the
// master key rotation (`rowbird keys rotate`), the one operation that must see every ciphertext
// at once; like SystemRepo it is not reachable from the API.
type SecretValueRepo struct{ s *Store }

// SecretValues returns the repository used by key rotation.
func (s *Store) SecretValues() *SecretValueRepo { return &SecretValueRepo{s: s} }

// List returns every encrypted value, including those of soft-deleted rows.
func (r *SecretValueRepo) List(ctx context.Context) ([]SecretValue, error) {
	db := r.s.conn(ctx)
	var out []SecretValue
	var users []struct {
		ID    uuid.UUID `bun:"id,type:uuid"`
		Value string    `bun:"totp_secret_enc"`
	}
	if err := db.NewSelect().Table("users").Column("id", "totp_secret_enc").
		Where("totp_secret_enc IS NOT NULL AND totp_secret_enc <> ''").OrderExpr("id").Scan(ctx, &users); err != nil {
		return nil, mapError(err)
	}
	for _, u := range users {
		out = append(out, SecretValue{Kind: SecretUserTOTP, ID: u.ID, Value: u.Value})
	}
	for _, t := range []struct{ kind, table string }{{SecretConnection, "connections"}, {SecretChannel, "channels"}} {
		var rows []struct {
			ID          uuid.UUID `bun:"id,type:uuid"`
			WorkspaceID uuid.UUID `bun:"workspace_id,type:uuid"`
			Value       string    `bun:"secrets_enc"`
		}
		if err := db.NewSelect().Table(t.table).Column("id", "workspace_id", "secrets_enc").
			Where("secrets_enc IS NOT NULL AND secrets_enc <> ''").OrderExpr("id").Scan(ctx, &rows); err != nil {
			return nil, mapError(err)
		}
		for _, row := range rows {
			out = append(out, SecretValue{Kind: t.kind, ID: row.ID, WorkspaceID: row.WorkspaceID, Value: row.Value})
		}
	}
	var settings []Setting
	if err := db.NewSelect().Model(&settings).Where("st.secret = ?", true).OrderExpr("st.id").Scan(ctx); err != nil {
		return nil, mapError(err)
	}
	for _, st := range settings {
		var v string
		if err := json.Unmarshal([]byte(st.Value), &v); err != nil {
			return nil, fmt.Errorf("store: secret setting %s is not a JSON string: %w", st.Key, err)
		}
		out = append(out, SecretValue{Kind: SecretSetting, ID: st.ID, WorkspaceID: st.WorkspaceID, Key: st.Key, Value: v})
	}
	return out, nil
}

// Replace stores a new ciphertext for v. Versions and update times are left alone: the value
// means the same thing, and concurrent editors must not see a conflict.
func (r *SecretValueRepo) Replace(ctx context.Context, v SecretValue, ciphertext string) error {
	db := r.s.conn(ctx)
	var err error
	switch v.Kind {
	case SecretUserTOTP:
		_, err = db.NewUpdate().Table("users").Set("totp_secret_enc = ?", ciphertext).Where("id = ?", v.ID).Exec(ctx)
	case SecretConnection:
		_, err = db.NewUpdate().Table("connections").Set("secrets_enc = ?", ciphertext).Where("id = ?", v.ID).Exec(ctx)
	case SecretChannel:
		_, err = db.NewUpdate().Table("channels").Set("secrets_enc = ?", ciphertext).Where("id = ?", v.ID).Exec(ctx)
	case SecretSetting:
		b, _ := json.Marshal(ciphertext)
		_, err = db.NewUpdate().Table("settings").Set("value = ?", string(b)).Where("id = ?", v.ID).Exec(ctx)
	default:
		return fmt.Errorf("store: unknown secret kind %q", v.Kind)
	}
	return mapError(err)
}
