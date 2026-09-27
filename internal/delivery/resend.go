package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/schedule"
	"github.com/rowbird/rowbird/internal/store"
)

// Errors of a resend.
var (
	ErrNotFailed     = apperr.New(apperr.KindConflict, "delivery.not_failed")
	ErrNotResendable = apperr.New(apperr.KindConflict, "delivery.not_resendable")
)

// MaxBulkResend caps how many attempts one bulk resend sends again.
const MaxBulkResend = 100

// Resend sends a failed attempt again with the run's stored files, without running the query. The
// attempt row is reused: its status goes back to sending and the new tries add to its count. When
// every attempt of a partial run has been sent, the run becomes a success.
func (e *Engine) Resend(ctx context.Context, attemptID uuid.UUID) (*store.DeliveryAttempt, error) {
	att, err := e.store.Attempts().Get(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	if att.Status != store.AttemptFailed {
		return nil, ErrNotFailed
	}
	// Test deliveries to the caller have no delivery row to resend with.
	if att.DeliveryID == nil {
		return nil, ErrNotResendable
	}
	d, err := e.store.Deliveries().Get(ctx, *att.DeliveryID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotResendable
	}
	if err != nil {
		return nil, err
	}
	rn, err := e.store.Runs().Get(ctx, att.RunID)
	if err != nil {
		return nil, err
	}
	rp, err := e.store.Reports().Get(ctx, rn.ReportID)
	if err != nil {
		return nil, err
	}
	status := plugin.StatusUp
	switch rn.Status {
	case store.RunSuccess, store.RunPartial:
	case store.RunSkipped:
		status = plugin.StatusSkipped
	case store.RunFailed:
		status = plugin.StatusDown
	default:
		return nil, ErrNotResendable
	}
	if rn.Trigger == store.TriggerTest {
		status = plugin.StatusUp
	}
	dest, env, ch, err := e.channels.Env(ctx, d.ChannelID)
	if err != nil {
		return nil, err
	}
	opts, err := dest.DeliverySchema().Validate(d.Options)
	if err != nil {
		return nil, err
	}
	env.Options, d.Options = opts, opts
	caps := plugin.CapabilitiesOf(dest, env.Config)
	loc, err := schedule.LoadLocation(rp.Timezone)
	if err != nil {
		loc = time.UTC
	}
	ok, err := e.store.Attempts().Claim(ctx, att.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFailed
	}
	att.Status = store.AttemptSending
	logger := e.logger.With("run_id", rn.ID, "report_id", rp.ID, "delivery_id", d.ID, "attempt_id", att.ID, "channel", ch.Name)
	logger.InfoContext(ctx, "resending a delivery")
	r := &run{e: e, in: Input{Run: rn, Report: rp, Status: status, Location: loc, Test: rn.Trigger == store.TriggerTest}, locale: e.locale(ctx), artifacts: map[string]*store.Artifact{}}
	env.Locale = r.locale
	msg, meta, err := r.message(ctx, *d, caps)
	if err != nil {
		code := plugin.ErrCodeDeliveryFailed
		var de *plugin.DeliveryError
		if errors.As(err, &de) {
			code = de.Code
		}
		message := err.Error()
		att.Status, att.LastErrorCode, att.LastError, att.Meta = store.AttemptFailed, &code, &message, meta
		if serr := e.store.Attempts().Save(ctx, att); serr != nil {
			return nil, serr
		}
		return att, nil
	}
	if e.send(ctx, att, dest, env, ch, msg, meta, logger) {
		if err := e.settle(ctx, rn); err != nil {
			logger.ErrorContext(ctx, "could not update the run after a resend", "error", err)
		}
	}
	if e.notifier != nil {
		e.notifier.RunUpdated(ctx, rn)
	}
	return att, nil
}

// settle turns a partial run into a success once all its attempts were sent.
func (e *Engine) settle(ctx context.Context, run *store.Run) error {
	if run.Status != store.RunPartial {
		return nil
	}
	atts, err := e.store.Attempts().ListByRun(ctx, run.ID)
	if err != nil {
		return err
	}
	for _, a := range atts {
		if a.Status != store.AttemptSent && a.Status != store.AttemptSkipped {
			return nil
		}
	}
	ok, err := e.store.Runs().Recover(ctx, run.ID)
	if ok {
		run.Status = store.RunSuccess
	}
	return err
}

// FailedAttempts lists the attempts of a channel that failed since the given time and can be sent
// again, at most MaxBulkResend of them.
func (e *Engine) FailedAttempts(ctx context.Context, channelID uuid.UUID, since time.Time) ([]uuid.UUID, error) {
	atts, err := e.store.Attempts().FailedByChannel(ctx, channelID, since)
	if err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for _, a := range atts {
		if a.DeliveryID != nil && len(out) < MaxBulkResend {
			out = append(out, a.ID)
		}
	}
	return out, nil
}
