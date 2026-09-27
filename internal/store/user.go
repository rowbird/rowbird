package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Role is a workspace role (docs/spec/05-api.md, "Permissions").
type Role string

// Roles, from least to most privileged.
const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleAdmin  Role = "admin"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleViewer || r == RoleEditor || r == RoleAdmin }

// User is a person who can sign in. Users are global; access to a workspace comes from Member.
type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`
	Base
	Email              string     `bun:"email,notnull"`
	Name               string     `bun:"name,notnull"`
	PasswordHash       *string    `bun:"password_hash"`
	Locale             string     `bun:"locale,notnull"`
	Theme              string     `bun:"theme,notnull"`
	TOTPSecretEnc      *string    `bun:"totp_secret_enc"`
	TOTPEnabled        bool       `bun:"totp_enabled,notnull"`
	TOTPLastStep       int64      `bun:"totp_last_step,notnull"`
	RecoveryCodes      []string   `bun:"recovery_codes"`
	MustChangePassword bool       `bun:"must_change_password,notnull"`
	FailedLogins       int        `bun:"failed_logins,notnull"`
	LockedUntil        *time.Time `bun:"locked_until"`
	DisabledAt         *time.Time `bun:"disabled_at"`
	LastLoginAt        *time.Time `bun:"last_login_at"`
	OIDCSubject        *string    `bun:"oidc_subject"`
	OIDCIssuer         *string    `bun:"oidc_issuer"`
}

// Disabled reports whether the account was disabled by an administrator.
func (u *User) Disabled() bool { return u.DisabledAt != nil }

// NormalizeEmail is the canonical form used for storage and lookups.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// UserRepo persists users. Users are global (see GlobalTables); listing the users of a workspace
// goes through MemberRepo.
type UserRepo struct{ s *Store }

// Users returns the user repository.
func (s *Store) Users() *UserRepo { return &UserRepo{s: s} }

// Create inserts u with a normalized email. A taken email returns ErrDuplicate.
func (r *UserRepo) Create(ctx context.Context, u *User) error {
	u.Email = NormalizeEmail(u.Email)
	prepareInsert(&u.Base)
	_, err := r.s.conn(ctx).NewInsert().Model(u).Exec(ctx)
	return mapError(err)
}

// Get returns the user with the given id.
func (r *UserRepo) Get(ctx context.Context, id uuid.UUID) (*User, error) {
	u := new(User)
	err := r.s.conn(ctx).NewSelect().Model(u).Where("u.id = ?", id).Scan(ctx)
	return u, mapError(err)
}

// GetByEmail looks a user up by email, ignoring case and surrounding spaces.
func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	u := new(User)
	err := r.s.conn(ctx).NewSelect().Model(u).Where("u.email = ?", NormalizeEmail(email)).Scan(ctx)
	return u, mapError(err)
}

// GetByOIDC returns the user linked to an identity at an OIDC provider.
func (r *UserRepo) GetByOIDC(ctx context.Context, issuer, subject string) (*User, error) {
	u := new(User)
	err := r.s.conn(ctx).NewSelect().Model(u).Where("u.oidc_issuer = ?", issuer).Where("u.oidc_subject = ?", subject).Scan(ctx)
	return u, mapError(err)
}

// GetMany returns the users with the given ids, in no particular order.
func (r *UserRepo) GetMany(ctx context.Context, ids []uuid.UUID) ([]User, error) {
	var users []User
	if len(ids) == 0 {
		return users, nil
	}
	err := r.s.conn(ctx).NewSelect().Model(&users).Where("u.id IN (?)", bun.List(ids)).Scan(ctx)
	return users, mapError(err)
}

// Any reports whether at least one user exists. The setup wizard is available only while it is false.
func (r *UserRepo) Any(ctx context.Context) (bool, error) {
	ok, err := r.s.conn(ctx).NewSelect().Model((*User)(nil)).Exists(ctx)
	return ok, mapError(err)
}

// Update saves the given columns with optimistic concurrency (see Scoped.Update).
func (r *UserRepo) Update(ctx context.Context, u *User, columns ...string) error {
	if u.Email != "" {
		u.Email = NormalizeEmail(u.Email)
	}
	return updateVersioned(ctx, r.s.conn(ctx), u, nil, columns)
}

// The login bookkeeping below changes on every sign-in attempt. It bypasses optimistic
// concurrency on purpose, so it never makes a concurrent profile edit fail with a conflict.

// IncrementFailedLogins atomically adds one failed attempt and returns the new count.
func (r *UserRepo) IncrementFailedLogins(ctx context.Context, id uuid.UUID) (int, error) {
	var n int
	err := r.s.conn(ctx).NewUpdate().Model((*User)(nil)).
		Set("failed_logins = failed_logins + 1").
		Where("id = ?", id).
		Returning("failed_logins").
		Scan(ctx, &n)
	return n, mapError(err)
}

// SetLockedUntil locks the account until t (nil unlocks it).
func (r *UserRepo) SetLockedUntil(ctx context.Context, id uuid.UUID, t *time.Time) error {
	_, err := r.s.conn(ctx).NewUpdate().Model((*User)(nil)).
		Set("locked_until = ?", t).Where("id = ?", id).Exec(ctx)
	return mapError(err)
}

// RecordLogin clears failed attempts and the lock, and records the sign-in time.
func (r *UserRepo) RecordLogin(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := r.s.conn(ctx).NewUpdate().Model((*User)(nil)).
		Set("failed_logins = 0").
		Set("locked_until = NULL").
		Set("last_login_at = ?", at).
		Where("id = ?", id).Exec(ctx)
	return mapError(err)
}

// UseTOTPStep records step as the last accepted TOTP time step. It returns false when step is not
// newer than the recorded one, which makes a code single-use even across instances.
func (r *UserRepo) UseTOTPStep(ctx context.Context, id uuid.UUID, step int64) (bool, error) {
	res, err := r.s.conn(ctx).NewUpdate().Model((*User)(nil)).
		Set("totp_last_step = ?", step).
		Where("id = ?", id).
		Where("totp_last_step < ?", step).
		Exec(ctx)
	if err != nil {
		return false, mapError(err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
