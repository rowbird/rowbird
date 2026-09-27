package reports

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/msgtemplate"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/schedule"
	"github.com/rowbird/rowbird/internal/store"
)

// Delivery defaults and limits (docs/spec/02-data-model.md).
const (
	DefaultInlineRows    = 20
	MaxInlineRows        = 200
	DefaultLinkExpiry    = 7 * 24 * 3600
	MinLinkExpiry        = 3600
	MaxLinkExpiry        = 90 * 24 * 3600
	MaxDeliveriesPerRept = 20
)

// DeliveryView is a delivery with its channel's name and type.
type DeliveryView struct {
	store.Delivery
	ChannelName string
	ChannelType string
}

// DeliveryInput creates or replaces a delivery.
type DeliveryInput struct {
	ChannelID              uuid.UUID
	Enabled                *bool
	Mode                   string
	Formats                []string
	InlineRowLimit         *int
	IncludeInlineWithFiles *bool
	LinkExpiresSeconds     *int
	LinkRequireLogin       *bool
	Options                map[string]any
}

// Deliveries lists a report's deliveries in order.
func (s *Service) Deliveries(ctx context.Context, reportID uuid.UUID) ([]DeliveryView, error) {
	if _, err := s.store.Reports().Get(ctx, reportID); err != nil {
		return nil, err
	}
	ds, err := s.store.Deliveries().ListByReport(ctx, reportID)
	if err != nil {
		return nil, err
	}
	out := make([]DeliveryView, len(ds))
	for i, d := range ds {
		out[i] = DeliveryView{Delivery: d}
		if ch, err := s.store.Channels().Get(ctx, d.ChannelID); err == nil {
			out[i].ChannelName, out[i].ChannelType = ch.Name, ch.Type
		}
	}
	return out, nil
}

func (s *Service) delivery(ctx context.Context, reportID, id uuid.UUID) (*DeliveryView, error) {
	d, err := s.store.Deliveries().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.ReportID != reportID {
		return nil, store.ErrNotFound
	}
	v := &DeliveryView{Delivery: *d}
	if ch, err := s.store.Channels().Get(ctx, d.ChannelID); err == nil {
		v.ChannelName, v.ChannelType = ch.Name, ch.Type
	}
	return v, nil
}

// CreateDelivery adds a delivery at the end of a report's list.
func (s *Service) CreateDelivery(ctx context.Context, reportID uuid.UUID, in DeliveryInput) (*DeliveryView, error) {
	if err := s.checkManagedReport(ctx, reportID); err != nil {
		return nil, err
	}
	existing, err := s.store.Deliveries().ListByReport(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if len(existing) >= MaxDeliveriesPerRept {
		return nil, apperr.Invalid(apperr.Field("report_id", "validation.too_many"))
	}
	d := &store.Delivery{
		ReportID: reportID, Enabled: true, InlineRowLimit: DefaultInlineRows, IncludeInlineWithFiles: true,
		LinkExpiresSeconds: DefaultLinkExpiry,
	}
	if len(existing) > 0 {
		d.Position = existing[len(existing)-1].Position + 1
	}
	if err := s.applyDelivery(ctx, d, in); err != nil {
		return nil, err
	}
	if err := s.store.Deliveries().Create(ctx, d); err != nil {
		return nil, err
	}
	return s.delivery(ctx, reportID, d.ID)
}

// UpdateDelivery replaces a delivery's settings with optimistic concurrency.
func (s *Service) UpdateDelivery(ctx context.Context, reportID, id uuid.UUID, version int64, in DeliveryInput) (*DeliveryView, error) {
	if err := s.checkManagedReport(ctx, reportID); err != nil {
		return nil, err
	}
	cur, err := s.delivery(ctx, reportID, id)
	if err != nil {
		return nil, err
	}
	d := cur.Delivery
	d.Version = version
	if err := s.applyDelivery(ctx, &d, in); err != nil {
		return nil, err
	}
	if err := s.store.Deliveries().Update(ctx, &d, "channel_id", "enabled", "mode", "formats", "inline_row_limit",
		"include_inline_with_files", "link_expires_seconds", "link_require_login", "options"); err != nil {
		return nil, err
	}
	return s.delivery(ctx, reportID, id)
}

// DeleteDelivery removes a delivery; past attempts stay on their runs.
func (s *Service) DeleteDelivery(ctx context.Context, reportID, id uuid.UUID) error {
	if err := s.checkManagedReport(ctx, reportID); err != nil {
		return err
	}
	if _, err := s.delivery(ctx, reportID, id); err != nil {
		return err
	}
	return s.store.Deliveries().Delete(ctx, id)
}

// applyDelivery validates in against the channel's destination and copies it into d.
func (s *Service) applyDelivery(ctx context.Context, d *store.Delivery, in DeliveryInput) error {
	var fe []apperr.FieldError
	ch, err := s.store.Channels().Get(ctx, in.ChannelID)
	if errors.Is(err, store.ErrNotFound) {
		return apperr.Invalid(apperr.Field("channel_id", "validation.invalid_value"))
	}
	if err != nil {
		return err
	}
	p, ok := plugin.Get(plugin.KindDestination, ch.Type)
	dest, isDest := p.(plugin.Destination)
	if !ok || !isDest {
		return apperr.Invalid(apperr.Field("channel_id", "validation.invalid_value"))
	}
	caps := plugin.CapabilitiesOf(dest, ch.Config)
	mode := in.Mode
	if mode == "" {
		mode = caps.Modes[0]
	}
	switch {
	case !slices.Contains(caps.Modes, mode):
		fe = append(fe, apperr.Field("mode", "validation.invalid_value"))
	case mode == plugin.ModeLink && s.baseURL == "":
		fe = append(fe, apperr.Field("mode", "validation.base_url_missing"))
	}
	formats := dedupe(in.Formats)
	for i, f := range formats {
		if !isFileFormat(f) {
			fe = append(fe, apperr.Field("formats["+strconv.Itoa(i)+"]", "validation.invalid_value"))
		}
	}
	if mode != plugin.ModeInline && len(formats) == 0 {
		fe = append(fe, apperr.Field("formats", "validation.required"))
	}
	opts, verr := dest.DeliverySchema().Validate(orEmpty(in.Options))
	if ae, ok := apperr.As(verr); ok {
		for _, f := range ae.Fields {
			fe = append(fe, apperr.Field("options."+f.Field, f.Code))
		}
	} else if verr != nil {
		return verr
	}
	for k, v := range opts {
		if str, ok := v.(string); ok && strings.Contains(str, "{{") {
			if err := msgtemplate.Validate(str); err != nil {
				code := msgtemplate.CodeSyntax
				if te, ok := msgtemplate.IsTemplateError(err); ok {
					code = te.Code
				}
				fe = append(fe, apperr.Field("options."+k, code))
			}
		}
	}
	if v, ok := dest.(plugin.DeliveryValidator); ok && verr == nil {
		if field, code, ok := v.ValidateDelivery(opts); !ok {
			fe = append(fe, apperr.Field("options."+field, code))
		}
	}
	d.ChannelID, d.Mode, d.Formats, d.Options = ch.ID, mode, formats, opts
	if mode == plugin.ModeInline {
		d.Formats = []string{}
	}
	if in.Enabled != nil {
		d.Enabled = *in.Enabled
	}
	if in.InlineRowLimit != nil {
		d.InlineRowLimit = *in.InlineRowLimit
	}
	if in.IncludeInlineWithFiles != nil {
		d.IncludeInlineWithFiles = *in.IncludeInlineWithFiles
	}
	if in.LinkExpiresSeconds != nil {
		d.LinkExpiresSeconds = *in.LinkExpiresSeconds
	}
	if in.LinkRequireLogin != nil {
		d.LinkRequireLogin = *in.LinkRequireLogin
	}
	fe = append(fe, checkRange("inline_row_limit", &d.InlineRowLimit, 1, MaxInlineRows)...)
	fe = append(fe, checkRange("link_expires_seconds", &d.LinkExpiresSeconds, MinLinkExpiry, MaxLinkExpiry)...)
	if len(fe) > 0 {
		return apperr.Invalid(fe...)
	}
	return nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func dedupe(in []string) []string {
	out := []string{}
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func isFileFormat(id string) bool {
	p, ok := plugin.Get(plugin.KindFormatter, id)
	if !ok {
		return false
	}
	c, _ := p.Capabilities().(plugin.FormatterCapabilities)
	return c.Kind == plugin.FormatterFile
}

// Deliverer is what the service needs from the delivery engine: previews and resends.
type Deliverer interface {
	Preview(ctx context.Context, rp *store.Report, run *store.Run, d store.Delivery, loc *time.Location) (plugin.Preview, error)
	Resend(ctx context.Context, attemptID uuid.UUID) (*store.DeliveryAttempt, error)
	FailedAttempts(ctx context.Context, channelID uuid.UUID, since time.Time) ([]uuid.UUID, error)
}

// PreviewDelivery renders what a delivery (saved or not) would send, with the report's latest
// result.
func (s *Service) PreviewDelivery(ctx context.Context, reportID uuid.UUID, in DeliveryInput) (*plugin.Preview, error) {
	rp, err := s.store.Reports().Get(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if s.deliverer == nil {
		return nil, errors.New("reports: no delivery engine")
	}
	d := store.Delivery{ReportID: reportID, Enabled: true, InlineRowLimit: DefaultInlineRows, IncludeInlineWithFiles: true, LinkExpiresSeconds: DefaultLinkExpiry}
	if err := s.applyDelivery(ctx, &d, in); err != nil {
		return nil, err
	}
	run, err := s.store.Runs().LatestWithResult(ctx, reportID)
	if errors.Is(err, store.ErrNotFound) {
		run = nil
	} else if err != nil {
		return nil, err
	}
	loc, err := schedule.LoadLocation(rp.Timezone)
	if err != nil {
		loc = time.UTC
	}
	p, err := s.deliverer.Preview(ctx, rp, run, d, loc)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// TestTarget is where a test run delivers.
type TestTarget struct {
	ChannelID *uuid.UUID `json:"channel_id,omitempty"`
	Email     string     `json:"email,omitempty"`
}

// Errors of test deliveries.
var (
	ErrNoSystemMailer = apperr.New(apperr.KindConflict, "delivery.no_system_mailer")
)

// TestDelivery runs a report now and delivers it only to the caller: by email through the system
// mailer, or to one channel through the report's deliveries on it. The run has trigger test,
// delivers even when the condition does not hold, and never counts toward failures.
func (s *Service) TestDelivery(ctx context.Context, p *auth.Principal, reportID uuid.UUID, channelID *uuid.UUID) (*store.Run, error) {
	if _, err := s.store.Reports().Get(ctx, reportID); err != nil {
		return nil, err
	}
	target := TestTarget{ChannelID: channelID}
	if channelID != nil {
		ds, err := s.store.Deliveries().ListByReport(ctx, reportID)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(ds, func(d store.Delivery) bool { return d.ChannelID == *channelID }) {
			return nil, apperr.Invalid(apperr.Field("channel_id", "validation.no_delivery"))
		}
	} else {
		if _, err := s.store.Channels().SystemMailer(ctx); errors.Is(err, store.ErrNotFound) {
			return nil, ErrNoSystemMailer
		} else if err != nil {
			return nil, err
		}
		u, err := s.store.Users().Get(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		target.Email = u.Email
	}
	raw, err := json.Marshal(target)
	if err != nil {
		return nil, err
	}
	at := s.clock()
	run := &store.Run{ReportID: reportID, Trigger: store.TriggerTest, TriggeredBy: &p.UserID, Deliver: true, AvailableAt: at, TestTarget: raw}
	run.CreatedBy, run.CreatedAt = &p.UserID, at
	if err := s.store.Runs().Create(ctx, run); err != nil {
		return nil, err
	}
	s.logger.InfoContext(ctx, "test delivery queued", "run_id", run.ID, "report_id", reportID)
	s.notifier.RunUpdated(ctx, run)
	s.wake()
	return run, nil
}

// checkManagedReport refuses delivery changes on a report the GitOps directory owns.
func (s *Service) checkManagedReport(ctx context.Context, reportID uuid.UUID) error {
	rp, err := s.store.Reports().Get(ctx, reportID)
	if err != nil {
		return err
	}
	return store.CheckManaged(ctx, rp.ManagedBy)
}
