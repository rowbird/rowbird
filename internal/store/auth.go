package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Session is a signed-in browser. Only the SHA-256 of the token is stored.
type Session struct {
	bun.BaseModel `bun:"table:sessions,alias:s"`
	TenantBase
	UserID     uuid.UUID  `bun:"user_id,notnull,type:uuid"`
	TokenHash  string     `bun:"token_hash,notnull"`
	ExpiresAt  time.Time  `bun:"expires_at,notnull"`
	LastSeenAt time.Time  `bun:"last_seen_at,notnull"`
	IP         string     `bun:"ip,notnull"`
	UserAgent  string     `bun:"user_agent,notnull"`
	RevokedAt  *time.Time `bun:"revoked_at"`
	// AuthMethod is how the session signed in: password, passkey or oidc.
	AuthMethod string `bun:"auth_method,notnull"`
}

// Active reports whether the session can still be used at t.
func (s *Session) Active(t time.Time) bool { return s.RevokedAt == nil && t.Before(s.ExpiresAt) }

// LoginChallenge is the pending second step of a sign-in (TOTP or recovery code).
type LoginChallenge struct {
	bun.BaseModel `bun:"table:login_challenges,alias:lc"`
	Base
	UserID    uuid.UUID `bun:"user_id,notnull,type:uuid"`
	TokenHash string    `bun:"token_hash,notnull"`
	ExpiresAt time.Time `bun:"expires_at,notnull"`
	Attempts  int       `bun:"attempts,notnull"`
}

// AuthRepo holds the lookups that authentication performs before any workspace is known: finding a
// session or an API key by token hash, and a user's memberships. They are deliberately unscoped;
// they are the only such exception (ADR-0015) and must not be used for anything else.
type AuthRepo struct{ s *Store }

// Auth returns the authentication repository.
func (s *Store) Auth() *AuthRepo { return &AuthRepo{s: s} }

// MembershipsOfUser lists every workspace membership of a user.
func (r *AuthRepo) MembershipsOfUser(ctx context.Context, userID uuid.UUID) ([]Member, error) {
	var ms []Member
	err := r.s.conn(ctx).NewSelect().Model(&ms).Where("m.user_id = ?", userID).OrderExpr("m.id").Scan(ctx)
	return ms, mapError(err)
}

// CreateSession stores a new session. WorkspaceID must already be set.
func (r *AuthRepo) CreateSession(ctx context.Context, s *Session) error {
	prepareInsert(&s.Base)
	_, err := r.s.conn(ctx).NewInsert().Model(s).Exec(ctx)
	return mapError(err)
}

// SessionByTokenHash returns the session with the given token hash, active or not.
func (r *AuthRepo) SessionByTokenHash(ctx context.Context, hash string) (*Session, error) {
	s := new(Session)
	err := r.s.conn(ctx).NewSelect().Model(s).Where("s.token_hash = ?", hash).Scan(ctx)
	return s, mapError(err)
}

// TouchSession slides the expiry of a session and records activity.
func (r *AuthRepo) TouchSession(ctx context.Context, id uuid.UUID, lastSeen, expires time.Time) error {
	_, err := r.s.conn(ctx).NewUpdate().Model((*Session)(nil)).
		Set("last_seen_at = ?", lastSeen).
		Set("expires_at = ?", expires).
		Where("id = ?", id).
		Where("revoked_at IS NULL").
		Exec(ctx)
	return mapError(err)
}

// ListActiveSessions returns the user's sessions that are neither revoked nor expired at t.
func (r *AuthRepo) ListActiveSessions(ctx context.Context, userID uuid.UUID, t time.Time) ([]Session, error) {
	var ss []Session
	err := r.s.conn(ctx).NewSelect().Model(&ss).
		Where("s.user_id = ?", userID).
		Where("s.revoked_at IS NULL").
		Where("s.expires_at > ?", t).
		OrderExpr("s.last_seen_at DESC").
		Scan(ctx)
	return ss, mapError(err)
}

// RevokeSession revokes one session of the user. It returns ErrNotFound when the session does not
// belong to the user or is already revoked.
func (r *AuthRepo) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID, at time.Time) error {
	res, err := r.s.conn(ctx).NewUpdate().Model((*Session)(nil)).
		Set("revoked_at = ?", at).
		Where("id = ?", sessionID).
		Where("user_id = ?", userID).
		Where("revoked_at IS NULL").
		Exec(ctx)
	if err != nil {
		return mapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeUserSessions revokes every active session of the user except the one with id except
// (uuid.Nil revokes all). It returns how many were revoked.
func (r *AuthRepo) RevokeUserSessions(ctx context.Context, userID, except uuid.UUID, at time.Time) (int, error) {
	q := r.s.conn(ctx).NewUpdate().Model((*Session)(nil)).
		Set("revoked_at = ?", at).
		Where("user_id = ?", userID).
		Where("revoked_at IS NULL")
	if except != uuid.Nil {
		q = q.Where("id <> ?", except)
	}
	res, err := q.Exec(ctx)
	if err != nil {
		return 0, mapError(err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CreateChallenge stores a login challenge.
func (r *AuthRepo) CreateChallenge(ctx context.Context, c *LoginChallenge) error {
	prepareInsert(&c.Base)
	_, err := r.s.conn(ctx).NewInsert().Model(c).Exec(ctx)
	return mapError(err)
}

// ChallengeByTokenHash returns a challenge by token hash.
func (r *AuthRepo) ChallengeByTokenHash(ctx context.Context, hash string) (*LoginChallenge, error) {
	c := new(LoginChallenge)
	err := r.s.conn(ctx).NewSelect().Model(c).Where("lc.token_hash = ?", hash).Scan(ctx)
	return c, mapError(err)
}

// IncrementChallengeAttempts atomically counts a failed attempt and returns the new total.
func (r *AuthRepo) IncrementChallengeAttempts(ctx context.Context, id uuid.UUID) (int, error) {
	var n int
	err := r.s.conn(ctx).NewUpdate().Model((*LoginChallenge)(nil)).
		Set("attempts = attempts + 1").
		Where("id = ?", id).
		Returning("attempts").
		Scan(ctx, &n)
	return n, mapError(err)
}

// DeleteChallenge removes a challenge once used or exhausted. Deleting also removes the user's
// expired challenges, so the table never grows.
func (r *AuthRepo) DeleteChallenge(ctx context.Context, c *LoginChallenge, t time.Time) error {
	_, err := r.s.conn(ctx).NewDelete().Model((*LoginChallenge)(nil)).
		WhereGroup(" AND ", func(q *bun.DeleteQuery) *bun.DeleteQuery {
			return q.Where("id = ?", c.ID).WhereOr("user_id = ? AND expires_at < ?", c.UserID, t)
		}).
		Exec(ctx)
	return mapError(err)
}

// APIKeyByHash returns the API key with the given hash, active or not.
func (r *AuthRepo) APIKeyByHash(ctx context.Context, hash string) (*APIKey, error) {
	k := new(APIKey)
	err := r.s.conn(ctx).NewSelect().Model(k).Where("k.key_hash = ?", hash).Scan(ctx)
	return k, mapError(err)
}

// TouchAPIKey records the last use of a key.
func (r *AuthRepo) TouchAPIKey(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := r.s.conn(ctx).NewUpdate().Model((*APIKey)(nil)).
		Set("last_used_at = ?", at).Where("id = ?", id).Exec(ctx)
	return mapError(err)
}
