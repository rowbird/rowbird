package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Delivery attempt statuses.
const (
	AttemptPending = "pending"
	AttemptSending = "sending"
	AttemptSent    = "sent"
	AttemptFailed  = "failed"
	AttemptSkipped = "skipped"
)

// Delivery sends a report's runs to a channel (docs/spec/02-data-model.md).
type Delivery struct {
	bun.BaseModel `bun:"table:deliveries,alias:dl"`
	TenantBase
	ReportID               uuid.UUID      `bun:"report_id,notnull,type:uuid"`
	ChannelID              uuid.UUID      `bun:"channel_id,notnull,type:uuid"`
	Position               int            `bun:"position,notnull"`
	Enabled                bool           `bun:"enabled,notnull"`
	Mode                   string         `bun:"mode,notnull"`
	Formats                []string       `bun:"formats,notnull"`
	InlineRowLimit         int            `bun:"inline_row_limit,notnull"`
	IncludeInlineWithFiles bool           `bun:"include_inline_with_files,notnull"`
	LinkExpiresSeconds     int            `bun:"link_expires_seconds,notnull"`
	LinkRequireLogin       bool           `bun:"link_require_login,notnull"`
	Options                map[string]any `bun:"options,notnull"`
}

// DeliveryRepo persists deliveries, scoped to the workspace in ctx.
type DeliveryRepo struct{ s *Store }

// Deliveries returns the delivery repository.
func (s *Store) Deliveries() *DeliveryRepo { return &DeliveryRepo{s: s} }

// Create stores a delivery.
func (r *DeliveryRepo) Create(ctx context.Context, d *Delivery) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	if d.Formats == nil {
		d.Formats = []string{}
	}
	if d.Options == nil {
		d.Options = map[string]any{}
	}
	return sc.Insert(ctx, d)
}

// Get returns a delivery by id.
func (r *DeliveryRepo) Get(ctx context.Context, id uuid.UUID) (*Delivery, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	d := new(Delivery)
	err = sc.NewSelect(d).Where("dl.id = ?", id).Scan(ctx)
	return d, mapError(err)
}

// ListByReport returns a report's deliveries in order.
func (r *DeliveryRepo) ListByReport(ctx context.Context, reportID uuid.UUID) ([]Delivery, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Delivery
	err = sc.NewSelect(&out).Where("dl.report_id = ?", reportID).OrderExpr("dl.position, dl.id").Scan(ctx)
	return out, mapError(err)
}

// ListByChannel returns the deliveries that use a channel.
func (r *DeliveryRepo) ListByChannel(ctx context.Context, channelID uuid.UUID) ([]Delivery, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Delivery
	err = sc.NewSelect(&out).Where("dl.channel_id = ?", channelID).OrderExpr("dl.id").Scan(ctx)
	return out, mapError(err)
}

// Update saves columns with optimistic concurrency.
func (r *DeliveryRepo) Update(ctx context.Context, d *Delivery, columns ...string) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	return sc.Update(ctx, d, columns...)
}

// Delete removes a delivery; its past attempts stay with the runs.
func (r *DeliveryRepo) Delete(ctx context.Context, id uuid.UUID) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	res, err := sc.NewDelete((*Delivery)(nil)).Where("dl.id = ?", id).Exec(ctx)
	if err != nil {
		return mapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeliveryAttempt is one delivery of one run, with its retries (docs/spec/02-data-model.md).
type DeliveryAttempt struct {
	bun.BaseModel `bun:"table:delivery_attempts,alias:da"`
	TenantBase
	RunID         uuid.UUID      `bun:"run_id,notnull,type:uuid"`
	DeliveryID    *uuid.UUID     `bun:"delivery_id,type:uuid"`
	ChannelID     *uuid.UUID     `bun:"channel_id,type:uuid"`
	Status        string         `bun:"status,notnull"`
	Attempts      int            `bun:"attempts,notnull"`
	LastErrorCode *string        `bun:"last_error_code"`
	LastError     *string        `bun:"last_error"`
	SentAt        *time.Time     `bun:"sent_at"`
	Meta          map[string]any `bun:"meta,notnull"`
}

// AttemptRepo persists delivery attempts, scoped to the workspace in ctx.
type AttemptRepo struct{ s *Store }

// Attempts returns the delivery attempt repository.
func (s *Store) Attempts() *AttemptRepo { return &AttemptRepo{s: s} }

// AnySent reports whether any delivery of the workspace was sent (the last onboarding step).
func (r *AttemptRepo) AnySent(ctx context.Context) (bool, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return false, err
	}
	ok, err := sc.NewSelect((*DeliveryAttempt)(nil)).Where("?TableAlias.status = ?", AttemptSent).Exists(ctx)
	return ok, mapError(err)
}

// Create stores an attempt.
func (r *AttemptRepo) Create(ctx context.Context, a *DeliveryAttempt) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	if a.Meta == nil {
		a.Meta = map[string]any{}
	}
	if a.Status == "" {
		a.Status = AttemptPending
	}
	return sc.Insert(ctx, a)
}

// Get returns an attempt by id.
func (r *AttemptRepo) Get(ctx context.Context, id uuid.UUID) (*DeliveryAttempt, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	a := new(DeliveryAttempt)
	err = sc.NewSelect(a).Where("da.id = ?", id).Scan(ctx)
	return a, mapError(err)
}

// ListByRun returns a run's attempts in creation order.
func (r *AttemptRepo) ListByRun(ctx context.Context, runID uuid.UUID) ([]DeliveryAttempt, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []DeliveryAttempt
	err = sc.NewSelect(&out).Where("da.run_id = ?", runID).OrderExpr("da.id").Scan(ctx)
	return out, mapError(err)
}

// FailedByChannel returns the failed attempts of a channel created since the given time.
func (r *AttemptRepo) FailedByChannel(ctx context.Context, channelID uuid.UUID, since time.Time) ([]DeliveryAttempt, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []DeliveryAttempt
	err = sc.NewSelect(&out).Where("da.channel_id = ?", channelID).Where("da.status = ?", AttemptFailed).
		Where("da.created_at >= ?", since.UTC()).OrderExpr("da.id").Scan(ctx)
	return out, mapError(err)
}

// Claim marks a failed attempt as sending again, so that two resends cannot send it twice. It
// reports whether the attempt was still failed.
func (r *AttemptRepo) Claim(ctx context.Context, id uuid.UUID) (bool, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return false, err
	}
	res, err := sc.NewUpdate((*DeliveryAttempt)(nil)).Set("status = ?", AttemptSending).Set("updated_at = ?", now()).
		Where("da.id = ?", id).Where("da.status = ?", AttemptFailed).Exec(ctx)
	if err != nil {
		return false, mapError(err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Save writes an attempt's progress. Attempts are only changed by the run that owns them or by a
// resend, one at a time, so the version is not checked.
func (r *AttemptRepo) Save(ctx context.Context, a *DeliveryAttempt) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	if a.Meta == nil {
		a.Meta = map[string]any{}
	}
	a.UpdatedAt = now()
	_, err = sc.NewUpdate(a).Column("status", "attempts", "last_error_code", "last_error", "sent_at", "meta", "updated_at").
		Where("da.id = ?", a.ID).Exec(ctx)
	return mapError(err)
}

// Artifact is a file a run produced, kept in the artifact storage (docs/spec/02-data-model.md).
type Artifact struct {
	bun.BaseModel `bun:"table:artifacts,alias:ar"`
	TenantBase
	RunID          uuid.UUID  `bun:"run_id,notnull,type:uuid"`
	Format         string     `bun:"format,notnull"`
	ContentType    string     `bun:"content_type,notnull"`
	FileName       string     `bun:"file_name,notnull"`
	StorageBackend string     `bun:"storage_backend,notnull"`
	StorageKey     string     `bun:"storage_key,notnull"`
	SizeBytes      int64      `bun:"size_bytes,notnull"`
	SHA256         string     `bun:"sha256,notnull"`
	ExpiresAt      *time.Time `bun:"expires_at"`
	DeletedAt      *time.Time `bun:"deleted_at"`
}

// ArtifactRepo persists artifacts, scoped to the workspace in ctx.
type ArtifactRepo struct{ s *Store }

// Artifacts returns the artifact repository.
func (s *Store) Artifacts() *ArtifactRepo { return &ArtifactRepo{s: s} }

// Create stores an artifact. A second artifact of the same format for a run is ErrDuplicate.
func (r *ArtifactRepo) Create(ctx context.Context, a *Artifact) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	return sc.Insert(ctx, a)
}

// Get returns an artifact by id.
func (r *ArtifactRepo) Get(ctx context.Context, id uuid.UUID) (*Artifact, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	a := new(Artifact)
	err = sc.NewSelect(a).Where("ar.id = ?", id).Scan(ctx)
	return a, mapError(err)
}

// ListByRun returns a run's artifacts that still exist.
func (r *ArtifactRepo) ListByRun(ctx context.Context, runID uuid.UUID) ([]Artifact, error) {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return nil, err
	}
	var out []Artifact
	err = sc.NewSelect(&out).Where("ar.run_id = ?", runID).Where("ar.deleted_at IS NULL").OrderExpr("ar.format").Scan(ctx)
	return out, mapError(err)
}
