package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Connection health states.
const (
	ConnectionUnknown = "unknown"
	ConnectionOK      = "ok"
	ConnectionError   = "error"
)

// Connection is a user database (docs/spec/02-data-model.md). Config holds non-secret settings;
// SecretsEnc holds the encrypted JSON of the secret ones.
type Connection struct {
	bun.BaseModel `bun:"table:connections,alias:cn"`
	TenantBase
	Name   string `bun:"name,notnull"`
	Driver string `bun:"driver,notnull"`
	// ManagedBy is ManagedByGitOps for resources the configuration directory owns.
	ManagedBy           string          `bun:"managed_by,notnull"`
	Config              map[string]any  `bun:"config,notnull"`
	SecretsEnc          *string         `bun:"secrets_enc"`
	QueryTimeoutSeconds int             `bun:"query_timeout_seconds,notnull"`
	MaxRows             int             `bun:"max_rows,notnull"`
	AllowMultiStatement bool            `bun:"allow_multi_statement,notnull"`
	AIExcludedTables    []string        `bun:"ai_excluded_tables,notnull"`
	SchemaCache         json.RawMessage `bun:"schema_cache,type:jsonb"`
	SchemaCachedAt      *time.Time      `bun:"schema_cached_at"`
	HasWritePermission  *bool           `bun:"has_write_permission"`
	Status              string          `bun:"status,notnull"`
	ServerVersion       string          `bun:"server_version,notnull"`
	LastCheckedAt       *time.Time      `bun:"last_checked_at"`
	LastError           string          `bun:"last_error,notnull"`
}

// ConnectionHealth is the result of the last connection check.
type ConnectionHealth struct {
	Status             string
	ServerVersion      string
	HasWritePermission *bool
	LastError          string
	CheckedAt          time.Time
}

// ConnectionRepo persists connections, scoped to the workspace in ctx.
type ConnectionRepo struct{ s *Store }

// Connections returns the connection repository.
func (s *Store) Connections() *ConnectionRepo { return &ConnectionRepo{s: s} }

// Create stores a new connection. A duplicate name returns ErrDuplicate. The id may be preset, so
// the caller can bind encrypted secrets to it before inserting.
func (r *ConnectionRepo) Create(ctx context.Context, c *Connection) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	if c.AIExcludedTables == nil {
		c.AIExcludedTables = []string{}
	}
	if c.Status == "" {
		c.Status = ConnectionUnknown
	}
	return sc.Insert(ctx, c)
}

// Get returns a connection by id.
func (r *ConnectionRepo) Get(ctx context.Context, id uuid.UUID) (*Connection, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	c := new(Connection)
	err = sc.NewSelect(c).Where("cn.id = ?", id).Scan(ctx)
	return c, mapError(err)
}

// List returns connections sorted by name. Schema caches are left out; they can be large.
func (r *ConnectionRepo) List(ctx context.Context) ([]Connection, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Connection
	err = sc.NewSelect(&out).ExcludeColumn("schema_cache").OrderExpr("cn.name").Scan(ctx)
	return out, mapError(err)
}

// Update saves the given columns with optimistic concurrency.
func (r *ConnectionRepo) Update(ctx context.Context, c *Connection, columns ...string) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	return sc.Update(ctx, c, columns...)
}

// SetHealth records a connection check without touching the version: it is bookkeeping, and must
// not make a concurrent edit fail.
func (r *ConnectionRepo) SetHealth(ctx context.Context, id uuid.UUID, h ConnectionHealth) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	q := sc.NewUpdate((*Connection)(nil)).
		Set("status = ?", h.Status).
		Set("last_error = ?", h.LastError).
		Set("last_checked_at = ?", h.CheckedAt).
		Where("cn.id = ?", id)
	if h.Status == ConnectionOK {
		q = q.Set("server_version = ?", h.ServerVersion).Set("has_write_permission = ?", h.HasWritePermission)
	}
	_, err = q.Exec(ctx)
	return mapError(err)
}

// SetSchemaCache stores an introspected schema (also bookkeeping, without a version bump).
func (r *ConnectionRepo) SetSchemaCache(ctx context.Context, id uuid.UUID, schema json.RawMessage, at time.Time) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	_, err = sc.NewUpdate((*Connection)(nil)).
		Set("schema_cache = ?", string(schema)).
		Set("schema_cached_at = ?", at).
		Where("cn.id = ?", id).
		Exec(ctx)
	return mapError(err)
}

// Delete removes a connection.
func (r *ConnectionRepo) Delete(ctx context.Context, id uuid.UUID) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	res, err := sc.NewDelete((*Connection)(nil)).Where("cn.id = ?", id).Exec(ctx)
	if err != nil {
		return mapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetByName returns the connection with that name, or ErrNotFound.
func (r *ConnectionRepo) GetByName(ctx context.Context, name string) (*Connection, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	v := new(Connection)
	err = sc.NewSelect(v).Where("cn.name = ?", name).Scan(ctx)
	return v, mapError(err)
}
