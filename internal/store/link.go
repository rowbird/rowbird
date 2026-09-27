package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// SharedLink gives access to an artifact through a secret token (docs/spec/07-security.md). Only
// the token's SHA-256 is stored.
type SharedLink struct {
	bun.BaseModel `bun:"table:shared_links,alias:sl"`
	TenantBase
	ArtifactID     uuid.UUID  `bun:"artifact_id,notnull,type:uuid"`
	RunID          uuid.UUID  `bun:"run_id,notnull,type:uuid"`
	DeliveryID     *uuid.UUID `bun:"delivery_id,type:uuid"`
	TokenHash      string     `bun:"token_hash,notnull"`
	ExpiresAt      time.Time  `bun:"expires_at,notnull"`
	RequireLogin   bool       `bun:"require_login,notnull"`
	RevokedAt      *time.Time `bun:"revoked_at"`
	RevokedBy      *uuid.UUID `bun:"revoked_by,type:uuid"`
	DownloadCount  int        `bun:"download_count,notnull"`
	LastDownloadAt *time.Time `bun:"last_download_at"`
}

// LinkDownload records one download through a shared link.
type LinkDownload struct {
	bun.BaseModel `bun:"table:link_downloads,alias:ld"`
	TenantBase
	LinkID    uuid.UUID  `bun:"link_id,notnull,type:uuid"`
	UserID    *uuid.UUID `bun:"user_id,type:uuid"`
	IP        string     `bun:"ip,notnull"`
	UserAgent string     `bun:"user_agent,notnull"`
}

// LinkFilter narrows a link listing.
type LinkFilter struct {
	RunID uuid.UUID
	// Active keeps only links that are neither revoked nor expired at the given time.
	ActiveAt time.Time
}

// LinkRepo persists shared links, scoped to the workspace in ctx.
type LinkRepo struct{ s *Store }

// Links returns the shared link repository.
func (s *Store) Links() *LinkRepo { return &LinkRepo{s: s} }

// Create stores a link.
func (r *LinkRepo) Create(ctx context.Context, l *SharedLink) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	return sc.Insert(ctx, l)
}

// Get returns a link by id.
func (r *LinkRepo) Get(ctx context.Context, id uuid.UUID) (*SharedLink, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	l := new(SharedLink)
	err = sc.NewSelect(l).Where("sl.id = ?", id).Scan(ctx)
	return l, mapError(err)
}

// List returns a page of links, newest first.
func (r *LinkRepo) List(ctx context.Context, f LinkFilter, page PageRequest) (Page[SharedLink], error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return Page[SharedLink]{}, err
	}
	var out []SharedLink
	q := sc.NewSelect(&out)
	if f.RunID != uuid.Nil {
		q = q.Where("sl.run_id = ?", f.RunID)
	}
	if !f.ActiveAt.IsZero() {
		q = q.Where("sl.revoked_at IS NULL").Where("sl.expires_at > ?", f.ActiveAt.UTC())
	}
	if q, err = paginate(q, page); err != nil {
		return Page[SharedLink]{}, err
	}
	if err := q.Scan(ctx); err != nil {
		return Page[SharedLink]{}, mapError(err)
	}
	return finishPage(out, page, func(l SharedLink) uuid.UUID { return l.ID }), nil
}

// Revoke ends a link. It reports false when the link was already revoked.
func (r *LinkRepo) Revoke(ctx context.Context, id, by uuid.UUID, at time.Time) (bool, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return false, err
	}
	res, err := sc.NewUpdate((*SharedLink)(nil)).Set("revoked_at = ?", at.UTC()).Set("revoked_by = ?", by).
		Where("sl.id = ?", id).Where("sl.revoked_at IS NULL").Exec(ctx)
	if err != nil {
		return false, mapError(err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RecordDownload counts a download and logs who made it.
func (r *LinkRepo) RecordDownload(ctx context.Context, d *LinkDownload) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	if err := sc.Insert(ctx, d); err != nil {
		return err
	}
	_, err = sc.NewUpdate((*SharedLink)(nil)).Set("download_count = sl.download_count + 1").
		Set("last_download_at = ?", d.CreatedAt).Where("sl.id = ?", d.LinkID).Exec(ctx)
	return mapError(err)
}

// Downloads returns a link's download log, newest first.
func (r *LinkRepo) Downloads(ctx context.Context, linkID uuid.UUID, limit int) ([]LinkDownload, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []LinkDownload
	err = sc.NewSelect(&out).Where("ld.link_id = ?", linkID).OrderExpr("ld.id DESC").Limit(limit).Scan(ctx)
	return out, mapError(err)
}

// LinkByTokenHash finds a link from anywhere: the public download route has no workspace until it
// finds the link. It returns a reference; the caller continues with the scoped repository.
func (r *SystemRepo) LinkByTokenHash(ctx context.Context, hash string) (Ref, error) {
	var ref Ref
	err := r.s.conn(ctx).NewSelect().Model((*SharedLink)(nil)).Column("sl.workspace_id", "sl.id").
		Where("sl.token_hash = ?", hash).Limit(1).Scan(ctx, &ref)
	return ref, mapError(err)
}
