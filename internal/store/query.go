package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Query is named, versioned SQL bound to a connection (docs/spec/02-data-model.md).
type Query struct {
	bun.BaseModel `bun:"table:queries,alias:q"`
	TenantBase
	Title       string `bun:"title,notnull"`
	Slug        string `bun:"slug,notnull"`
	Description string `bun:"description,notnull"`
	// ManagedBy is ManagedByGitOps for resources the configuration directory owns.
	ManagedBy        string     `bun:"managed_by,notnull"`
	ConnectionID     uuid.UUID  `bun:"connection_id,notnull,type:uuid"`
	CurrentVersionID *uuid.UUID `bun:"current_version_id,type:uuid"`
}

// QueryParam is a user-defined parameter as stored in a version (see internal/params).
type QueryParam struct {
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	Default *string `json:"default,omitempty"`
}

// QueryVersion is an immutable revision of a query's SQL and parameters.
type QueryVersion struct {
	bun.BaseModel `bun:"table:query_versions,alias:qv"`
	TenantBase
	QueryID uuid.UUID    `bun:"query_id,notnull,type:uuid"`
	Number  int          `bun:"number,notnull"`
	SQL     string       `bun:"sql,notnull"`
	Params  []QueryParam `bun:"params,notnull"`
	Note    string       `bun:"note,notnull"`
	// RestoredFrom is the version number this one was copied from by a restore.
	RestoredFrom *int `bun:"restored_from"`
}

// QueryRepo persists queries and their versions, scoped to the workspace in ctx.
type QueryRepo struct{ s *Store }

// Queries returns the query repository.
func (s *Store) Queries() *QueryRepo { return &QueryRepo{s: s} }

// Create stores a query. A taken slug returns ErrDuplicate.
func (r *QueryRepo) Create(ctx context.Context, q *Query) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	return sc.Insert(ctx, q)
}

// Get returns a query by id.
func (r *QueryRepo) Get(ctx context.Context, id uuid.UUID) (*Query, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	q := new(Query)
	err = sc.NewSelect(q).Where("q.id = ?", id).Scan(ctx)
	return q, mapError(err)
}

// List returns every query, most recently changed first.
func (r *QueryRepo) List(ctx context.Context) ([]Query, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Query
	err = sc.NewSelect(&out).OrderExpr("q.updated_at DESC").Scan(ctx)
	return out, mapError(err)
}

// ListByConnection returns the queries that use a connection.
func (r *QueryRepo) ListByConnection(ctx context.Context, connectionID uuid.UUID) ([]Query, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Query
	err = sc.NewSelect(&out).Where("q.connection_id = ?", connectionID).OrderExpr("q.title").Scan(ctx)
	return out, mapError(err)
}

// Update saves columns with optimistic concurrency.
func (r *QueryRepo) Update(ctx context.Context, q *Query, columns ...string) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	return sc.Update(ctx, q, columns...)
}

// Delete removes a query and, by cascade, its versions.
func (r *QueryRepo) Delete(ctx context.Context, id uuid.UUID) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	if _, err := sc.NewDelete((*QueryVersion)(nil)).Where("qv.query_id = ?", id).Exec(ctx); err != nil {
		return mapError(err)
	}
	res, err := sc.NewDelete((*Query)(nil)).Where("q.id = ?", id).Exec(ctx)
	if err != nil {
		return mapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddVersion stores v as the next version of its query (number = last + 1). Two concurrent saves
// cannot get the same number: the unique constraint makes one of them fail with ErrDuplicate.
func (r *QueryRepo) AddVersion(ctx context.Context, v *QueryVersion) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	var last sql.NullInt64
	err = sc.NewSelect((*QueryVersion)(nil)).ColumnExpr("MAX(qv.number)").Where("qv.query_id = ?", v.QueryID).Scan(ctx, &last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return mapError(err)
	}
	v.Number = int(last.Int64) + 1
	if v.Params == nil {
		v.Params = []QueryParam{}
	}
	return sc.Insert(ctx, v)
}

// Version returns one version of a query by number.
func (r *QueryRepo) Version(ctx context.Context, queryID uuid.UUID, number int) (*QueryVersion, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	v := new(QueryVersion)
	err = sc.NewSelect(v).Where("qv.query_id = ?", queryID).Where("qv.number = ?", number).Scan(ctx)
	return v, mapError(err)
}

// VersionByID returns a version by id.
func (r *QueryRepo) VersionByID(ctx context.Context, id uuid.UUID) (*QueryVersion, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	v := new(QueryVersion)
	err = sc.NewSelect(v).Where("qv.id = ?", id).Scan(ctx)
	return v, mapError(err)
}

// Versions lists a query's versions, newest first, without their SQL.
func (r *QueryRepo) Versions(ctx context.Context, queryID uuid.UUID) ([]QueryVersion, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []QueryVersion
	err = sc.NewSelect(&out).ExcludeColumn("sql").Where("qv.query_id = ?", queryID).OrderExpr("qv.number DESC").Scan(ctx)
	return out, mapError(err)
}

// GetBySlug returns the query with that slug, or ErrNotFound.
func (r *QueryRepo) GetBySlug(ctx context.Context, slug string) (*Query, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	v := new(Query)
	err = sc.NewSelect(v).Where("q.slug = ?", slug).Scan(ctx)
	return v, mapError(err)
}
