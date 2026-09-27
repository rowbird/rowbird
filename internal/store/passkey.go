package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Passkey is a WebAuthn credential of a user (docs/spec/02-data-model.md). Credential holds the
// credential record as JSON; CredentialID is its id in base64url, unique across users.
type Passkey struct {
	bun.BaseModel `bun:"table:passkeys,alias:pk"`
	Base
	UserID       uuid.UUID  `bun:"user_id,notnull,type:uuid"`
	Name         string     `bun:"name,notnull"`
	CredentialID string     `bun:"credential_id,notnull"`
	Credential   string     `bun:"credential,notnull"`
	SignCount    int64      `bun:"sign_count,notnull"`
	LastUsedAt   *time.Time `bun:"last_used_at"`
}

// WebAuthnChallenge is the server side of a ceremony in progress.
type WebAuthnChallenge struct {
	bun.BaseModel `bun:"table:webauthn_challenges,alias:wc"`
	Base
	TokenHash string     `bun:"token_hash,notnull"`
	Purpose   string     `bun:"purpose,notnull"`
	UserID    *uuid.UUID `bun:"user_id,type:uuid"`
	Data      string     `bun:"data,notnull"`
	ExpiresAt time.Time  `bun:"expires_at,notnull"`
}

// PasskeyRepo persists passkeys and WebAuthn challenges. Like users, they are global: every lookup
// is by user id or by credential id.
type PasskeyRepo struct{ s *Store }

// Passkeys returns the passkey repository.
func (s *Store) Passkeys() *PasskeyRepo { return &PasskeyRepo{s: s} }

// ListByUser returns a user's passkeys, oldest first.
func (r *PasskeyRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]Passkey, error) {
	var out []Passkey
	err := r.s.conn(ctx).NewSelect().Model(&out).Where("pk.user_id = ?", userID).OrderExpr("pk.created_at, pk.id").Scan(ctx)
	return out, mapError(err)
}

// CountByUser counts a user's passkeys.
func (r *PasskeyRepo) CountByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	n, err := r.s.conn(ctx).NewSelect().Model((*Passkey)(nil)).Where("pk.user_id = ?", userID).Count(ctx)
	return n, mapError(err)
}

// Get returns one of the user's passkeys.
func (r *PasskeyRepo) Get(ctx context.Context, userID, id uuid.UUID) (*Passkey, error) {
	p := new(Passkey)
	err := r.s.conn(ctx).NewSelect().Model(p).Where("pk.id = ?", id).Where("pk.user_id = ?", userID).Scan(ctx)
	return p, mapError(err)
}

// Create stores a passkey. A credential id already registered is ErrDuplicate.
func (r *PasskeyRepo) Create(ctx context.Context, p *Passkey) error {
	prepareInsert(&p.Base)
	_, err := r.s.conn(ctx).NewInsert().Model(p).Exec(ctx)
	return mapError(err)
}

// Rename changes a passkey's name.
func (r *PasskeyRepo) Rename(ctx context.Context, p *Passkey) error {
	return updateVersioned(ctx, r.s.conn(ctx), p, nil, []string{"name"})
}

// RecordUse stores the credential record after a successful assertion (its sign count and flags).
func (r *PasskeyRepo) RecordUse(ctx context.Context, id uuid.UUID, credential string, signCount int64, at time.Time) error {
	_, err := r.s.conn(ctx).NewUpdate().Model((*Passkey)(nil)).
		Set("credential = ?", credential).Set("sign_count = ?", signCount).Set("last_used_at = ?", at.UTC()).
		Set("updated_at = ?", now()).Set("version = pk.version + 1").
		Where("pk.id = ?", id).Exec(ctx)
	return mapError(err)
}

// Delete removes one of the user's passkeys.
func (r *PasskeyRepo) Delete(ctx context.Context, userID, id uuid.UUID) error {
	res, err := r.s.conn(ctx).NewDelete().Model((*Passkey)(nil)).Where("pk.id = ?", id).Where("pk.user_id = ?", userID).Exec(ctx)
	if n, _ := affected(res, err); err == nil && n == 0 {
		return ErrNotFound
	}
	return mapError(err)
}

// DeleteByUser removes every passkey of a user and returns how many there were.
func (r *PasskeyRepo) DeleteByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	res, err := r.s.conn(ctx).NewDelete().Model((*Passkey)(nil)).Where("pk.user_id = ?", userID).Exec(ctx)
	return affected(res, err)
}

// PutChallenge stores a ceremony, replacing an earlier one with the same token and purpose.
func (r *PasskeyRepo) PutChallenge(ctx context.Context, c *WebAuthnChallenge) error {
	return r.s.RunInTx(ctx, func(ctx context.Context) error {
		db := r.s.conn(ctx)
		if _, err := db.NewDelete().Model((*WebAuthnChallenge)(nil)).
			Where("wc.token_hash = ?", c.TokenHash).Where("wc.purpose = ?", c.Purpose).Exec(ctx); err != nil {
			return mapError(err)
		}
		prepareInsert(&c.Base)
		_, err := db.NewInsert().Model(c).Exec(ctx)
		return mapError(err)
	})
}

// TakeChallenge removes and returns the ceremony with the token hash and purpose. Each ceremony
// can be answered once; an expired one is ErrNotFound.
func (r *PasskeyRepo) TakeChallenge(ctx context.Context, tokenHash, purpose string, at time.Time) (*WebAuthnChallenge, error) {
	c := new(WebAuthnChallenge)
	err := r.s.RunInTx(ctx, func(ctx context.Context) error {
		db := r.s.conn(ctx)
		if err := db.NewSelect().Model(c).Where("wc.token_hash = ?", tokenHash).Where("wc.purpose = ?", purpose).Scan(ctx); err != nil {
			return mapError(err)
		}
		res, err := db.NewDelete().Model((*WebAuthnChallenge)(nil)).Where("wc.id = ?", c.ID).Exec(ctx)
		if n, _ := affected(res, err); err == nil && n == 0 {
			return ErrNotFound
		}
		return mapError(err)
	})
	if err != nil {
		return nil, err
	}
	if !at.Before(c.ExpiresAt) {
		return nil, ErrNotFound
	}
	return c, nil
}

// DeleteExpiredWebAuthnChallenges removes ceremonies that expired before t.
func (r *MaintenanceRepo) DeleteExpiredWebAuthnChallenges(ctx context.Context, t time.Time) (int, error) {
	res, err := r.s.conn(ctx).NewDelete().Model((*WebAuthnChallenge)(nil)).Where("wc.expires_at < ?", t.UTC()).Exec(ctx)
	return affected(res, err)
}
