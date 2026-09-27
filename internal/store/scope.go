package store

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/rowbird/rowbird/internal/store/ids"
)

type workspaceKey struct{}

// WithWorkspace binds a workspace to ctx. The API does this once per request after authorization;
// repositories read it back through Scoped.
func WithWorkspace(ctx context.Context, workspaceID uuid.UUID) context.Context {
	return context.WithValue(ctx, workspaceKey{}, workspaceID)
}

// WorkspaceFrom returns the workspace bound to ctx.
func WorkspaceFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(workspaceKey{}).(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// Scoped builds queries restricted to one workspace. It is the only way repositories touch
// tenant-owned tables: every select, update and delete it builds carries the workspace filter, and
// every insert writes the workspace id. See ADR-0007 and ADR-0015.
type Scoped struct {
	db          bun.IDB
	workspaceID uuid.UUID
}

// Scoped returns a query builder for the workspace bound to ctx, inside the transaction bound to
// ctx if there is one. A context without a workspace is an error: there is no unscoped fallback.
func (s *Store) Scoped(ctx context.Context) (*Scoped, error) {
	id, ok := WorkspaceFrom(ctx)
	if !ok {
		return nil, ErrNoWorkspace
	}
	return &Scoped{db: s.conn(ctx), workspaceID: id}, nil
}

// WorkspaceID is the workspace this builder is bound to.
func (sc *Scoped) WorkspaceID() uuid.UUID { return sc.workspaceID }

// NewSelect starts a select on model restricted to the workspace.
func (sc *Scoped) NewSelect(model any) *bun.SelectQuery {
	return sc.db.NewSelect().Model(model).Where("?TableAlias.workspace_id = ?", sc.workspaceID)
}

// NewUpdate starts an update on model restricted to the workspace.
func (sc *Scoped) NewUpdate(model any) *bun.UpdateQuery {
	return sc.db.NewUpdate().Model(model).Where("?TableAlias.workspace_id = ?", sc.workspaceID)
}

// NewDelete starts a delete on model restricted to the workspace.
func (sc *Scoped) NewDelete(model any) *bun.DeleteQuery {
	return sc.db.NewDelete().Model(model).Where("?TableAlias.workspace_id = ?", sc.workspaceID)
}

// Insert stores a new tenant-owned model. It assigns the id (unless set), timestamps, version 1
// and, unconditionally, the workspace of this builder.
func (sc *Scoped) Insert(ctx context.Context, model tenantOwned) error {
	prepareInsert(model.base())
	model.tenant().WorkspaceID = sc.workspaceID
	_, err := sc.db.NewInsert().Model(model).Exec(ctx)
	return mapError(err)
}

// Update writes the given columns of a tenant-owned model with optimistic concurrency: it only
// succeeds if the stored version equals model's version, and increments it. It returns
// ErrConflict when the version is stale and ErrNotFound when the row does not exist in this
// workspace.
func (sc *Scoped) Update(ctx context.Context, model tenantOwned, columns ...string) error {
	return updateVersioned(ctx, sc.db, model, &sc.workspaceID, columns)
}

// prepareInsert keeps a CreatedAt the caller set (services with their own clock do), truncated
// like the store clock.
func prepareInsert(b *Base) {
	if b.ID == uuid.Nil {
		b.ID = ids.New()
	}
	t := now()
	if !b.CreatedAt.IsZero() {
		t = b.CreatedAt.UTC().Truncate(time.Microsecond)
	}
	b.CreatedAt, b.UpdatedAt, b.Version = t, t, 1
}

// updateVersioned implements optimistic concurrency for both scoped and global tables. A nil
// workspaceID means the table is global (see GlobalTables).
func updateVersioned(ctx context.Context, db bun.IDB, model versioned, workspaceID *uuid.UUID, columns []string) error {
	b := model.base()
	expected, prevUpdated := b.Version, b.UpdatedAt
	b.Version, b.UpdatedAt = expected+1, now()

	q := db.NewUpdate().Model(model).
		Column(slices.Concat(columns, []string{"version", "updated_at"})...).
		Where("?TableAlias.id = ?", b.ID).
		Where("?TableAlias.version = ?", expected)
	if workspaceID != nil {
		q = q.Where("?TableAlias.workspace_id = ?", *workspaceID)
	}
	res, err := q.Exec(ctx)
	if err == nil {
		var n int64
		if n, err = res.RowsAffected(); err == nil && n == 1 {
			return nil
		}
	}
	b.Version, b.UpdatedAt = expected, prevUpdated
	if err != nil {
		return mapError(err)
	}

	exists := db.NewSelect().Model(model).Where("?TableAlias.id = ?", b.ID)
	if workspaceID != nil {
		exists = exists.Where("?TableAlias.workspace_id = ?", *workspaceID)
	}
	found, err := exists.Exists(ctx)
	if err != nil {
		return fmt.Errorf("store: check existence after update: %w", err)
	}
	if found {
		return ErrConflict
	}
	return ErrNotFound
}
