package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// GlobalTables are the only tables without a workspace_id column: the tenant table itself, users
// (global per docs/spec/02-data-model.md; membership is per workspace), login challenges (created
// before a workspace is known) and migration bookkeeping. An architecture test fails if any other
// table lacks workspace_id.
var GlobalTables = []string{"workspaces", "users", "login_challenges", "instances", "maintenance_jobs", "passkeys", "webauthn_challenges", "password_resets", MigrationTable, migrationLockTable}

// Workspace is the tenant boundary. The OSS edition has exactly one, created by the setup wizard.
type Workspace struct {
	bun.BaseModel `bun:"table:workspaces,alias:w"`
	Base
	Name string `bun:"name,notnull"`
	Slug string `bun:"slug,notnull"`
}

// WorkspaceRepo persists workspaces. Workspaces are global, so this repository is not scoped.
type WorkspaceRepo struct{ s *Store }

// Workspaces returns the workspace repository.
func (s *Store) Workspaces() *WorkspaceRepo { return &WorkspaceRepo{s: s} }

// Create inserts w, assigning id, timestamps and version.
func (r *WorkspaceRepo) Create(ctx context.Context, w *Workspace) error {
	prepareInsert(&w.Base)
	_, err := r.s.conn(ctx).NewInsert().Model(w).Exec(ctx)
	return mapError(err)
}

// Get returns the workspace with the given id.
func (r *WorkspaceRepo) Get(ctx context.Context, id uuid.UUID) (*Workspace, error) {
	w := new(Workspace)
	err := r.s.conn(ctx).NewSelect().Model(w).Where("w.id = ?", id).Scan(ctx)
	return w, mapError(err)
}

// List returns every workspace, oldest first (instance-wide jobs such as the heartbeat).
func (r *WorkspaceRepo) List(ctx context.Context) ([]Workspace, error) {
	var out []Workspace
	err := r.s.conn(ctx).NewSelect().Model(&out).OrderExpr("w.id").Scan(ctx)
	return out, mapError(err)
}

// GetBySlug returns the workspace with the given slug.
func (r *WorkspaceRepo) GetBySlug(ctx context.Context, slug string) (*Workspace, error) {
	w := new(Workspace)
	err := r.s.conn(ctx).NewSelect().Model(w).Where("w.slug = ?", slug).Scan(ctx)
	return w, mapError(err)
}

// Update saves name and slug with optimistic concurrency (see Scoped.Update).
func (r *WorkspaceRepo) Update(ctx context.Context, w *Workspace) error {
	return updateVersioned(ctx, r.s.conn(ctx), w, nil, []string{"name", "slug"})
}
