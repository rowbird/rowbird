package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Notification severities.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// Notification is an in-app notification for one user (docs/spec/02-data-model.md). Occurrences
// with the same GroupKey are counted on the open notification of the group until it is resolved.
type Notification struct {
	bun.BaseModel `bun:"table:notifications,alias:nt"`
	TenantBase
	UserID     uuid.UUID      `bun:"user_id,notnull,type:uuid"`
	Type       string         `bun:"type,notnull"`
	Severity   string         `bun:"severity,notnull"`
	TitleKey   string         `bun:"title_key,notnull"`
	Params     map[string]any `bun:"params,notnull"`
	EntityType string         `bun:"entity_type,notnull"`
	EntityID   *uuid.UUID     `bun:"entity_id,type:uuid"`
	GroupKey   string         `bun:"group_key,notnull"`
	Count      int            `bun:"count,notnull"`
	FirstAt    time.Time      `bun:"first_at,notnull"`
	LastAt     time.Time      `bun:"last_at,notnull"`
	ReadAt     *time.Time     `bun:"read_at"`
	ResolvedAt *time.Time     `bun:"resolved_at"`
}

// NotificationRepo persists notifications, scoped to the workspace in ctx.
type NotificationRepo struct{ s *Store }

// Notifications returns the notification repository.
func (s *Store) Notifications() *NotificationRepo { return &NotificationRepo{s: s} }

// Record stores an occurrence for n.UserID at n.LastAt. With a group key and an open notification
// of that group, the occurrence is counted on it (it becomes unread again and takes n's params);
// otherwise n is inserted. It returns the stored notification and whether it is new.
func (r *NotificationRepo) Record(ctx context.Context, n *Notification) (*Notification, bool, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, false, err
	}
	at := n.LastAt.UTC()
	if n.GroupKey != "" {
		// Two attempts: a concurrent insert of the same group can win the unique index.
		for range 2 {
			open, err := r.open(ctx, sc, n.UserID, n.GroupKey)
			if err == nil {
				if n.Params == nil {
					n.Params = open.Params
				}
				params, jerr := json.Marshal(n.Params)
				if jerr != nil {
					return nil, false, jerr
				}
				_, err = sc.NewUpdate((*Notification)(nil)).
					Set("count = nt.count + 1").Set("last_at = ?", at).Set("read_at = NULL").
					Set("params = ?", string(params)).Set("updated_at = ?", now()).
					Where("nt.id = ?", open.ID).Exec(ctx)
				if err != nil {
					return nil, false, mapError(err)
				}
				open.Count++
				open.LastAt, open.ReadAt, open.Params = at, nil, n.Params
				return open, false, nil
			}
			if !errors.Is(err, ErrNotFound) {
				return nil, false, err
			}
			if err = r.insert(ctx, sc, n, at); err == nil {
				return n, true, nil
			}
			if !errors.Is(err, ErrDuplicate) {
				return nil, false, err
			}
		}
		return nil, false, ErrConflict
	}
	if err := r.insert(ctx, sc, n, at); err != nil {
		return nil, false, err
	}
	return n, true, nil
}

func (r *NotificationRepo) insert(ctx context.Context, sc *Scoped, n *Notification, at time.Time) error {
	if n.Params == nil {
		n.Params = map[string]any{}
	}
	if n.Count == 0 {
		n.Count = 1
	}
	if n.FirstAt.IsZero() {
		n.FirstAt = at
	}
	n.LastAt = at
	n.ID = uuid.Nil
	return sc.Insert(ctx, n)
}

func (r *NotificationRepo) open(ctx context.Context, sc *Scoped, userID uuid.UUID, group string) (*Notification, error) {
	n := new(Notification)
	err := sc.NewSelect(n).Where("nt.user_id = ?", userID).Where("nt.group_key = ?", group).
		Where("nt.resolved_at IS NULL").Limit(1).Scan(ctx)
	return n, mapError(err)
}

// HasOpen reports whether any user has an open notification of the group.
func (r *NotificationRepo) HasOpen(ctx context.Context, group string) (bool, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return false, err
	}
	n, err := sc.NewSelect((*Notification)(nil)).Where("nt.group_key = ?", group).
		Where("nt.resolved_at IS NULL").Count(ctx)
	return n > 0, mapError(err)
}

// Resolve closes the open notifications of a group. It reports whether there were any.
func (r *NotificationRepo) Resolve(ctx context.Context, group string, at time.Time) (bool, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return false, err
	}
	res, err := sc.NewUpdate((*Notification)(nil)).Set("resolved_at = ?", at.UTC()).Set("updated_at = ?", now()).
		Where("nt.group_key = ?", group).Where("nt.resolved_at IS NULL").Exec(ctx)
	if err != nil {
		return false, mapError(err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Get returns one of a user's notifications.
func (r *NotificationRepo) Get(ctx context.Context, userID, id uuid.UUID) (*Notification, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	n := new(Notification)
	err = sc.NewSelect(n).Where("nt.id = ?", id).Where("nt.user_id = ?", userID).Scan(ctx)
	return n, mapError(err)
}

// List returns a page of a user's notifications, newest first.
func (r *NotificationRepo) List(ctx context.Context, userID uuid.UUID, unreadOnly bool, page PageRequest) (Page[Notification], error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return Page[Notification]{}, err
	}
	var out []Notification
	q := sc.NewSelect(&out).Where("nt.user_id = ?", userID)
	if unreadOnly {
		q = q.Where("nt.read_at IS NULL")
	}
	if q, err = paginate(q, page); err != nil {
		return Page[Notification]{}, err
	}
	if err := q.Scan(ctx); err != nil {
		return Page[Notification]{}, mapError(err)
	}
	return finishPage(out, page, func(n Notification) uuid.UUID { return n.ID }), nil
}

// UnreadCount counts a user's unread notifications.
func (r *NotificationRepo) UnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return 0, err
	}
	n, err := sc.NewSelect((*Notification)(nil)).Where("nt.user_id = ?", userID).Where("nt.read_at IS NULL").Count(ctx)
	return n, mapError(err)
}

// MarkRead marks one of a user's notifications as read. It returns ErrNotFound for another user's.
func (r *NotificationRepo) MarkRead(ctx context.Context, userID, id uuid.UUID, at time.Time) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	res, err := sc.NewUpdate((*Notification)(nil)).Set("read_at = COALESCE(nt.read_at, ?)", at.UTC()).
		Where("nt.id = ?", id).Where("nt.user_id = ?", userID).Exec(ctx)
	if err != nil {
		return mapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllRead marks every unread notification of a user as read and returns how many there were.
func (r *NotificationRepo) MarkAllRead(ctx context.Context, userID uuid.UUID, at time.Time) (int, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return 0, err
	}
	res, err := sc.NewUpdate((*Notification)(nil)).Set("read_at = ?", at.UTC()).
		Where("nt.user_id = ?", userID).Where("nt.read_at IS NULL").Exec(ctx)
	if err != nil {
		return 0, mapError(err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ActiveAdminIDs returns the admins of the workspace whose account is not disabled.
func (r *MemberRepo) ActiveAdminIDs(ctx context.Context) ([]uuid.UUID, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []uuid.UUID
	err = sc.NewSelect((*Member)(nil)).Column("m.user_id").
		Join("JOIN users AS u ON u.id = m.user_id").
		Where("m.role = ?", RoleAdmin).Where("u.disabled_at IS NULL").
		OrderExpr("m.user_id").Scan(ctx, &out)
	return out, mapError(err)
}
