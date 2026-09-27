package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PasswordReset is a "forgot password" link (docs/spec/02-data-model.md): global, like users.
type PasswordReset struct {
	bun.BaseModel `bun:"table:password_resets,alias:pr"`
	Base
	UserID    uuid.UUID  `bun:"user_id,notnull,type:uuid"`
	TokenHash string     `bun:"token_hash,notnull"`
	ExpiresAt time.Time  `bun:"expires_at,notnull"`
	UsedAt    *time.Time `bun:"used_at"`
}

// ReplacePasswordReset stores a new reset for the user and removes their earlier ones.
func (r *AuthRepo) ReplacePasswordReset(ctx context.Context, pr *PasswordReset) error {
	return r.s.RunInTx(ctx, func(ctx context.Context) error {
		db := r.s.conn(ctx)
		if _, err := db.NewDelete().Model((*PasswordReset)(nil)).Where("pr.user_id = ?", pr.UserID).Exec(ctx); err != nil {
			return mapError(err)
		}
		prepareInsert(&pr.Base)
		_, err := db.NewInsert().Model(pr).Exec(ctx)
		return mapError(err)
	})
}

// LatestPasswordReset returns when the user last asked for a reset, nil when never.
func (r *AuthRepo) LatestPasswordReset(ctx context.Context, userID uuid.UUID) (*time.Time, error) {
	pr := new(PasswordReset)
	err := r.s.conn(ctx).NewSelect().Model(pr).Where("pr.user_id = ?", userID).OrderExpr("pr.created_at DESC").Limit(1).Scan(ctx)
	if err := mapError(err); errors.Is(err, ErrNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &pr.CreatedAt, nil
}

// UsePasswordReset marks the reset with the token hash as used and returns it, when it exists, is
// unused and has not expired at t. Two concurrent uses cannot both succeed.
func (r *AuthRepo) UsePasswordReset(ctx context.Context, tokenHash string, t time.Time) (*PasswordReset, error) {
	pr := new(PasswordReset)
	res, err := r.s.conn(ctx).NewUpdate().Model(pr).Set("used_at = ?", t.UTC()).Set("updated_at = ?", now()).
		Where("pr.token_hash = ?", tokenHash).Where("pr.used_at IS NULL").Where("pr.expires_at > ?", t.UTC()).
		Returning("*").Exec(ctx)
	if n, aerr := affected(res, err); aerr != nil {
		return nil, aerr
	} else if n == 0 {
		return nil, ErrNotFound
	}
	return pr, nil
}

// DeleteEndedPasswordResets removes resets that expired or were used before t.
func (r *MaintenanceRepo) DeleteEndedPasswordResets(ctx context.Context, t time.Time) (int, error) {
	res, err := r.s.conn(ctx).NewDelete().Model((*PasswordReset)(nil)).
		Where("pr.expires_at < ? OR pr.used_at IS NOT NULL", t.UTC()).Exec(ctx)
	return affected(res, err)
}
