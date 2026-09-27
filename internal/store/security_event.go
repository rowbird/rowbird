package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// SecurityEvent records a security-relevant action (docs/spec/07-security.md). Meta must never
// contain secrets.
type SecurityEvent struct {
	bun.BaseModel `bun:"table:security_events,alias:se"`
	TenantBase
	ActorUserID *uuid.UUID     `bun:"actor_user_id,type:uuid"`
	Type        string         `bun:"type,notnull"`
	IP          string         `bun:"ip,notnull"`
	Meta        map[string]any `bun:"meta"`
}

// SecurityEventFilter narrows a listing. Empty fields do not filter.
type SecurityEventFilter struct {
	Type string
}

// SecurityEventRepo persists security events, scoped to the workspace in ctx.
type SecurityEventRepo struct{ s *Store }

// SecurityEvents returns the security event repository.
func (s *Store) SecurityEvents() *SecurityEventRepo { return &SecurityEventRepo{s: s} }

// Record stores an event.
func (r *SecurityEventRepo) Record(ctx context.Context, e *SecurityEvent) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	return sc.Insert(ctx, e)
}

// List returns events newest first.
func (r *SecurityEventRepo) List(ctx context.Context, f SecurityEventFilter, req PageRequest) (Page[SecurityEvent], error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return Page[SecurityEvent]{}, err
	}
	var events []SecurityEvent
	q := sc.NewSelect(&events)
	if f.Type != "" {
		q = q.Where("se.type = ?", f.Type)
	}
	if q, err = paginate(q, req); err != nil {
		return Page[SecurityEvent]{}, err
	}
	if err := q.Scan(ctx); err != nil {
		return Page[SecurityEvent]{}, mapError(err)
	}
	return finishPage(events, req, func(e SecurityEvent) uuid.UUID { return e.ID }), nil
}
