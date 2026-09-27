package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Channel health.
const (
	ChannelUnknown = "unknown"
	ChannelOK      = "ok"
	ChannelFailing = "failing"
)

// Channel is a configured destination (docs/spec/02-data-model.md).
type Channel struct {
	bun.BaseModel `bun:"table:channels,alias:ch"`
	TenantBase
	Name string `bun:"name,notnull"`
	Type string `bun:"type,notnull"`
	// ManagedBy is ManagedByGitOps for resources the configuration directory owns.
	ManagedBy      string         `bun:"managed_by,notnull"`
	Config         map[string]any `bun:"config,notnull"`
	SecretsEnc     *string        `bun:"secrets_enc"`
	Status         string         `bun:"status,notnull"`
	LastSuccessAt  *time.Time     `bun:"last_success_at"`
	LastFailureAt  *time.Time     `bun:"last_failure_at"`
	LastError      *string        `bun:"last_error"`
	IsSystemMailer bool           `bun:"is_system_mailer,notnull"`
}

// ChannelRepo persists channels, scoped to the workspace in ctx.
type ChannelRepo struct{ s *Store }

// Channels returns the channel repository.
func (s *Store) Channels() *ChannelRepo { return &ChannelRepo{s: s} }

// Create stores a channel. A taken name returns ErrDuplicate.
func (r *ChannelRepo) Create(ctx context.Context, c *Channel) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	if c.Status == "" {
		c.Status = ChannelUnknown
	}
	if c.Config == nil {
		c.Config = map[string]any{}
	}
	return sc.Insert(ctx, c)
}

// Get returns a channel by id.
func (r *ChannelRepo) Get(ctx context.Context, id uuid.UUID) (*Channel, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	c := new(Channel)
	err = sc.NewSelect(c).Where("ch.id = ?", id).Scan(ctx)
	return c, mapError(err)
}

// List returns the channels by name.
func (r *ChannelRepo) List(ctx context.Context) ([]Channel, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Channel
	err = sc.NewSelect(&out).OrderExpr("ch.name").Scan(ctx)
	return out, mapError(err)
}

// SystemMailer returns the email channel marked for system mail, or ErrNotFound.
func (r *ChannelRepo) SystemMailer(ctx context.Context) (*Channel, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	c := new(Channel)
	err = sc.NewSelect(c).Where("ch.is_system_mailer = ?", true).OrderExpr("ch.id").Limit(1).Scan(ctx)
	return c, mapError(err)
}

// ClearSystemMailer unmarks every channel but keep.
func (r *ChannelRepo) ClearSystemMailer(ctx context.Context, keep uuid.UUID) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	_, err = sc.NewUpdate((*Channel)(nil)).Set("is_system_mailer = ?", false).
		Where("ch.is_system_mailer = ?", true).Where("ch.id <> ?", keep).Exec(ctx)
	return mapError(err)
}

// Update saves columns with optimistic concurrency.
func (r *ChannelRepo) Update(ctx context.Context, c *Channel, columns ...string) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	return sc.Update(ctx, c, columns...)
}

// RecordHealth stores the outcome of a send or a test and returns the status before it, so that
// callers can tell a channel that started failing or recovered. It does not bump the version.
func (r *ChannelRepo) RecordHealth(ctx context.Context, id uuid.UUID, ok bool, at time.Time, message string) (string, error) {
	var prev string
	err := r.s.RunInTx(ctx, func(ctx context.Context) error {
		sc, err := r.s.Scoped(ctx)
		if err != nil {
			return err
		}
		if err := sc.NewSelect((*Channel)(nil)).Column("ch.status").Where("ch.id = ?", id).Scan(ctx, &prev); err != nil {
			return mapError(err)
		}
		q := sc.NewUpdate((*Channel)(nil)).Where("ch.id = ?", id)
		if ok {
			q = q.Set("status = ?", ChannelOK).Set("last_success_at = ?", at)
		} else {
			q = q.Set("status = ?", ChannelFailing).Set("last_failure_at = ?", at).Set("last_error = ?", message)
		}
		_, err = q.Exec(ctx)
		return mapError(err)
	})
	return prev, err
}

// Delete removes a channel. Deliveries reference channels, so a channel in use cannot be deleted
// (the service checks first and reports what uses it).
func (r *ChannelRepo) Delete(ctx context.Context, id uuid.UUID) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	res, err := sc.NewDelete((*Channel)(nil)).Where("ch.id = ?", id).Exec(ctx)
	if err != nil {
		return mapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetByName returns the channel with that name, or ErrNotFound.
func (r *ChannelRepo) GetByName(ctx context.Context, name string) (*Channel, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	v := new(Channel)
	err = sc.NewSelect(v).Where("ch.name = ?", name).Scan(ctx)
	return v, mapError(err)
}
