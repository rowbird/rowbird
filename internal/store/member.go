package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Member grants a user a role in a workspace.
type Member struct {
	bun.BaseModel `bun:"table:workspace_members,alias:m"`
	TenantBase
	UserID uuid.UUID `bun:"user_id,notnull,type:uuid"`
	Role   Role      `bun:"role,notnull"`
}

// MemberUser is a member together with its user.
type MemberUser struct {
	Member Member
	User   User
}

// MemberRepo persists workspace memberships. Every method is scoped to the workspace in ctx.
type MemberRepo struct{ s *Store }

// Members returns the membership repository.
func (s *Store) Members() *MemberRepo { return &MemberRepo{s: s} }

// Add creates a membership in the workspace bound to ctx.
func (r *MemberRepo) Add(ctx context.Context, m *Member) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	return sc.Insert(ctx, m)
}

// GetByUser returns the membership of userID in the workspace bound to ctx.
func (r *MemberRepo) GetByUser(ctx context.Context, userID uuid.UUID) (*Member, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	m := new(Member)
	err = sc.NewSelect(m).Where("m.user_id = ?", userID).Scan(ctx)
	return m, mapError(err)
}

// Update saves the role with optimistic concurrency.
func (r *MemberRepo) Update(ctx context.Context, m *Member) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	return sc.Update(ctx, m, "role")
}

// CountActiveAdmins counts admins whose account is not disabled.
func (r *MemberRepo) CountActiveAdmins(ctx context.Context) (int, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return 0, err
	}
	n, err := sc.NewSelect((*Member)(nil)).
		Join("JOIN users AS u ON u.id = m.user_id").
		Where("m.role = ?", RoleAdmin).
		Where("u.disabled_at IS NULL").
		Count(ctx)
	return n, mapError(err)
}

// List returns the members of the workspace with their users, newest first.
func (r *MemberRepo) List(ctx context.Context, req PageRequest) (Page[MemberUser], error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return Page[MemberUser]{}, err
	}
	var members []Member
	q, err := paginate(sc.NewSelect(&members), req)
	if err != nil {
		return Page[MemberUser]{}, err
	}
	if err := q.Scan(ctx); err != nil {
		return Page[MemberUser]{}, mapError(err)
	}
	page := finishPage(members, req, func(m Member) uuid.UUID { return m.ID })

	ids := make([]uuid.UUID, len(page.Items))
	for i, m := range page.Items {
		ids[i] = m.UserID
	}
	users, err := r.s.Users().GetMany(ctx, ids)
	if err != nil {
		return Page[MemberUser]{}, err
	}
	byID := make(map[uuid.UUID]User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	out := Page[MemberUser]{NextCursor: page.NextCursor, Items: make([]MemberUser, 0, len(page.Items))}
	for _, m := range page.Items {
		out.Items = append(out.Items, MemberUser{Member: m, User: byID[m.UserID]})
	}
	return out, nil
}
