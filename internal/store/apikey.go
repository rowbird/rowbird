package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// APIKey authenticates automation (docs/spec/05-api.md). Only the SHA-256 of the key is stored.
type APIKey struct {
	bun.BaseModel `bun:"table:api_keys,alias:k"`
	TenantBase
	UserID     uuid.UUID  `bun:"user_id,notnull,type:uuid"`
	Name       string     `bun:"name,notnull"`
	Prefix     string     `bun:"prefix,notnull"`
	KeyHash    string     `bun:"key_hash,notnull"`
	Scopes     []string   `bun:"scopes,notnull"`
	ExpiresAt  *time.Time `bun:"expires_at"`
	LastUsedAt *time.Time `bun:"last_used_at"`
	RevokedAt  *time.Time `bun:"revoked_at"`
}

// Active reports whether the key can be used at t.
func (k *APIKey) Active(t time.Time) bool {
	return k.RevokedAt == nil && (k.ExpiresAt == nil || t.Before(*k.ExpiresAt))
}

// APIKeyRepo persists API keys, scoped to the workspace in ctx.
type APIKeyRepo struct{ s *Store }

// APIKeys returns the API key repository.
func (s *Store) APIKeys() *APIKeyRepo { return &APIKeyRepo{s: s} }

// Create stores a new key.
func (r *APIKeyRepo) Create(ctx context.Context, k *APIKey) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	return sc.Insert(ctx, k)
}

// Get returns a key by id.
func (r *APIKeyRepo) Get(ctx context.Context, id uuid.UUID) (*APIKey, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	k := new(APIKey)
	err = sc.NewSelect(k).Where("k.id = ?", id).Scan(ctx)
	return k, mapError(err)
}

// List returns the keys of the workspace, newest first.
func (r *APIKeyRepo) List(ctx context.Context, req PageRequest) (Page[APIKey], error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return Page[APIKey]{}, err
	}
	var keys []APIKey
	q, err := paginate(sc.NewSelect(&keys), req)
	if err != nil {
		return Page[APIKey]{}, err
	}
	if err := q.Scan(ctx); err != nil {
		return Page[APIKey]{}, mapError(err)
	}
	return finishPage(keys, req, func(k APIKey) uuid.UUID { return k.ID }), nil
}

// Revoke marks the key as revoked. Revoking twice returns ErrNotFound.
func (r *APIKeyRepo) Revoke(ctx context.Context, id uuid.UUID, at time.Time) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	res, err := sc.NewUpdate((*APIKey)(nil)).
		Set("revoked_at = ?", at).
		Set("updated_at = ?", at).
		Set("version = version + 1").
		Where("k.id = ?", id).
		Where("k.revoked_at IS NULL").
		Exec(ctx)
	if err != nil {
		return mapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeAllOfUser revokes every active key owned by the user, used when the user is disabled.
func (r *APIKeyRepo) RevokeAllOfUser(ctx context.Context, userID uuid.UUID, at time.Time) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	_, err = sc.NewUpdate((*APIKey)(nil)).
		Set("revoked_at = ?", at).
		Set("updated_at = ?", at).
		Set("version = version + 1").
		Where("k.user_id = ?", userID).
		Where("k.revoked_at IS NULL").
		Exec(ctx)
	return mapError(err)
}
