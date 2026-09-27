package notify

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/logging"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/store"
)

// alert is a system alert waiting to be sent.
type alert struct {
	workspace uuid.UUID
	// skip is a channel that must not carry this alert (the channel the alert is about).
	skip uuid.UUID
	// owner, when set, also receives the alert by email through the system mailer.
	owner   *uuid.UUID
	key     string
	params  map[string]any
	sev     string
	recover bool
	path    string
}

func channelGroup(id uuid.UUID) string { return "channel:" + id.String() + ":failing" }
func reportGroup(id uuid.UUID) string  { return "report:" + id.String() + ":failing" }
func pausedGroup(id uuid.UUID) string  { return "report:" + id.String() + ":paused" }

// ChannelHealthRecorded turns channel outcomes into notifications: the first failure opens a
// channel_failing notification and alerts, later failures are counted on it, and the first
// success after failures resolves it with a recovery notice and message.
func (s *Service) ChannelHealthRecorded(ctx context.Context, c *store.Channel, prev string, ok bool, message string) {
	s.publish(ctx, EventChannelHealth, nil, map[string]any{"channel_id": c.ID.String(), "ok": ok})
	at := s.clock()
	group := channelGroup(c.ID)
	params := map[string]any{"channel": c.Name, "channel_type": c.Type}
	if !ok {
		open, err := s.store.Notifications().HasOpen(ctx, group)
		if err != nil {
			s.logger.ErrorContext(ctx, "could not read notifications", "error", err)
			return
		}
		params["error"] = message
		s.record(ctx, occurrence{
			typ: TypeChannelFailing, severity: store.SeverityError, group: group, entityType: "channel", entityID: c.ID,
			params: params, users: s.admins(ctx),
		}, at)
		if !open {
			s.logger.WarnContext(ctx, "channel started failing", "channel", c.Name)
			s.alert(ctx, alert{skip: c.ID, key: "channelFailing", params: params, sev: plugin.AlertError, path: "/channels/" + c.ID.String()})
		}
		return
	}
	if prev != store.ChannelFailing {
		return
	}
	resolved, err := s.store.Notifications().Resolve(ctx, group, at)
	if err != nil || !resolved {
		return
	}
	s.logger.InfoContext(ctx, "channel recovered", "channel", c.Name)
	s.record(ctx, occurrence{
		typ: TypeChannelRecovered, severity: store.SeverityInfo, entityType: "channel", entityID: c.ID, params: params, users: s.admins(ctx),
	}, at)
	s.alert(ctx, alert{key: "channelRecovered", params: params, sev: plugin.AlertInfo, recover: true, path: "/channels/" + c.ID.String()})
}

// RunFinished handles a run that counts toward the report's health (scheduled runs and manual
// runs that deliver). paused says the run made the report pause itself.
func (s *Service) RunFinished(ctx context.Context, run *store.Run, rp *store.Report, paused bool) {
	at := s.clock()
	users := s.admins(ctx)
	var owner *uuid.UUID
	if rp.NotifyOwnerOnFail && rp.OwnerID != nil {
		owner = rp.OwnerID
		if !contains(users, *owner) {
			users = append(users, *owner)
		}
	}
	params := map[string]any{"report": rp.Title, "run_id": run.ID.String()}
	path := "/reports/" + rp.ID.String()
	switch run.Status {
	case store.RunFailed:
		if run.ErrorCode != nil {
			params["error_code"] = *run.ErrorCode
		}
		group := reportGroup(rp.ID)
		open, err := s.store.Notifications().HasOpen(ctx, group)
		if err != nil {
			s.logger.ErrorContext(ctx, "could not read notifications", "error", err)
			return
		}
		s.record(ctx, occurrence{
			typ: TypeReportFailing, severity: store.SeverityError, group: group, entityType: "report", entityID: rp.ID, params: params, users: users,
		}, at)
		if !open {
			s.alert(ctx, alert{owner: owner, key: "reportFailing", params: params, sev: plugin.AlertError, path: "/runs/" + run.ID.String()})
		}
		if paused {
			pp := map[string]any{"report": rp.Title, "failures": rp.AutoPauseAfter}
			s.record(ctx, occurrence{
				typ: TypeReportPaused, severity: store.SeverityWarning, group: pausedGroup(rp.ID), entityType: "report", entityID: rp.ID, params: pp, users: users,
			}, at)
			s.alert(ctx, alert{owner: owner, key: "reportPaused", params: pp, sev: plugin.AlertWarning, path: path})
			s.ReportUpdated(ctx, rp.ID)
		}
	case store.RunSuccess, store.RunPartial, store.RunSkipped:
		resolved, err := s.store.Notifications().Resolve(ctx, reportGroup(rp.ID), at)
		if err != nil || !resolved {
			return
		}
		s.record(ctx, occurrence{
			typ: TypeReportRecovered, severity: store.SeverityInfo, entityType: "report", entityID: rp.ID, params: params, users: users,
		}, at)
		s.alert(ctx, alert{owner: owner, key: "reportRecovered", params: params, sev: plugin.AlertInfo, recover: true, path: path})
	}
}

// ReportResumed closes the "paused" notification of a report someone resumed.
func (s *Service) ReportResumed(ctx context.Context, reportID uuid.UUID) {
	if _, err := s.store.Notifications().Resolve(ctx, pausedGroup(reportID), s.clock()); err != nil {
		s.logger.ErrorContext(ctx, "could not resolve a notification", "error", err)
	}
	s.ReportUpdated(ctx, reportID)
}

func contains(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// alert sends a, now when the service is synchronous, otherwise through the background queue.
func (s *Service) alert(ctx context.Context, a alert) {
	ws, ok := store.WorkspaceFrom(ctx)
	if !ok {
		return
	}
	a.workspace = ws
	if s.sync {
		s.send(context.WithoutCancel(ctx), a)
		return
	}
	select {
	case s.queue <- a:
	default:
		s.logger.ErrorContext(ctx, "too many alerts waiting; one was dropped", "alert", a.key)
	}
}

// Run sends queued alerts until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case a := <-s.queue:
			s.send(ctx, a)
		}
	}
}

// send delivers an alert to the primary channel, or to the fallback when the primary is not set,
// is the channel the alert is about, or fails. The owner's email goes through the system mailer.
// Alert sends do not change channel health, so an alert about a channel cannot cause another.
func (s *Service) send(ctx context.Context, a alert) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	ctx = store.WithWorkspace(ctx, a.workspace)
	locale := s.locale(ctx)
	msg := s.message(a, locale)
	settings, err := s.GetSettings(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "could not read alert settings", "error", err)
		return
	}
	sent := false
	for i, target := range []*Target{settings.Primary, settings.Fallback} {
		if target == nil || target.ChannelID == a.skip {
			continue
		}
		if err := s.deliver(ctx, target.ChannelID, target.Options, msg); err != nil {
			s.logger.WarnContext(ctx, "system alert failed", "alert", a.key, "fallback", i == 1, "error", err)
			continue
		}
		s.logger.InfoContext(ctx, "system alert sent", "alert", a.key, "fallback", i == 1)
		sent = true
		break
	}
	if !sent && (settings.Primary != nil || settings.Fallback != nil) {
		s.logger.ErrorContext(ctx, "no channel could carry a system alert", "alert", a.key)
	}
	if a.owner != nil {
		s.emailOwner(ctx, a, locale)
	}
}

func (s *Service) emailOwner(ctx context.Context, a alert, locale string) {
	mailer, err := s.store.Channels().SystemMailer(ctx)
	if err != nil {
		return
	}
	u, err := s.store.Users().Get(ctx, *a.owner)
	if err != nil || u.DisabledAt != nil {
		return
	}
	// The owner reads the email in their own language.
	if u.Locale != "" {
		locale = u.Locale
	}
	if err := s.deliver(ctx, mailer.ID, map[string]any{"to": u.Email}, s.message(a, locale)); err != nil {
		s.logger.WarnContext(ctx, "could not email the report owner", "error", err)
	}
}

func (s *Service) message(a alert, locale string) plugin.Message {
	return plugin.Message{Locale: locale, Alert: &plugin.MessageAlert{
		Title: t(locale, "notify."+a.key+".title", a.params), Text: t(locale, "notify."+a.key+".text", a.params),
		Severity: a.sev, Recovered: a.recover, URL: s.url(a.path),
	}}
}

// deliver sends msg through a channel with the given delivery options.
func (s *Service) deliver(ctx context.Context, channelID uuid.UUID, options map[string]any, msg plugin.Message) error {
	dest, env, _, err := s.channels.Env(ctx, channelID)
	if err != nil {
		return err
	}
	if !plugin.CapabilitiesOf(dest, env.Config).SupportsAlerts {
		return errors.New("notify: the channel cannot carry alerts")
	}
	opts, err := dest.DeliverySchema().Validate(options)
	if err != nil {
		return err
	}
	env.Options, env.Locale = opts, msg.Locale
	sctx, cancel := context.WithTimeout(ctx, AlertTimeout)
	defer cancel()
	_, err = dest.Send(sctx, env, msg)
	if err != nil {
		return errors.New(channels.SafeMessage(dest, env, err))
	}
	return nil
}

// GitOpsApplied tells admins about the configuration directory: a failure (err), which stays open
// until an apply succeeds, and reports paused because their documents left the directory.
func (s *Service) GitOpsApplied(ctx context.Context, err error, orphaned []store.Report) {
	at := s.clock()
	const group = "gitops:failed"
	if err != nil {
		msg := err.Error()
		if len(msg) > 1000 {
			msg = msg[:1000] + "..."
		}
		s.record(ctx, occurrence{typ: TypeGitOpsFailed, severity: store.SeverityError, group: group, params: map[string]any{"error": msg}, users: s.admins(ctx)}, at)
		return
	}
	if _, rerr := s.store.Notifications().Resolve(ctx, group, at); rerr != nil {
		s.logger.ErrorContext(ctx, "could not resolve notifications", "error", rerr)
	}
	for _, rp := range orphaned {
		s.record(ctx, occurrence{
			typ: TypeReportOrphaned, severity: store.SeverityWarning, group: "report:" + rp.ID.String() + ":orphaned",
			entityType: "report", entityID: rp.ID, params: map[string]any{"report": rp.Title}, users: s.admins(ctx),
		}, at)
	}
}

// BackupFinished records a failed scheduled backup for the admins of every workspace, and resolves
// that notification when a later backup succeeds. Backups belong to the instance, not to one
// workspace, so ctx carries none.
func (s *Service) BackupFinished(ctx context.Context, err error) {
	workspaces, lerr := s.store.Workspaces().List(ctx)
	if lerr != nil {
		s.logger.ErrorContext(ctx, "could not list workspaces", "error", lerr)
		return
	}
	at := s.clock()
	const group = "backup:failed"
	for _, w := range workspaces {
		wctx := store.WithWorkspace(ctx, w.ID)
		if err == nil {
			if _, rerr := s.store.Notifications().Resolve(wctx, group, at); rerr != nil {
				s.logger.ErrorContext(ctx, "could not resolve notifications", "error", rerr)
			}
			continue
		}
		msg := logging.RedactString(err.Error())
		if len(msg) > 1000 {
			msg = msg[:1000] + "..."
		}
		s.record(wctx, occurrence{typ: TypeBackupFailed, severity: store.SeverityError, group: group, params: map[string]any{"error": msg}, users: s.admins(wctx)}, at)
	}
}

// PasswordResetAvailable reports whether the workspace in ctx has a system mailer to send reset
// links through.
func (s *Service) PasswordResetAvailable(ctx context.Context) bool {
	_, err := s.store.Channels().SystemMailer(ctx)
	return err == nil
}

// SendPasswordReset emails a "forgot password" link to the user, in their language, through the
// system mailer of the workspace in ctx.
func (s *Service) SendPasswordReset(ctx context.Context, user *store.User, link string) error {
	mailer, err := s.store.Channels().SystemMailer(ctx)
	if err != nil {
		return err
	}
	locale := user.Locale
	if locale == "" {
		locale = s.locale(ctx)
	}
	msg := plugin.Message{Locale: locale, Alert: &plugin.MessageAlert{
		Title: t(locale, "notify.passwordReset.title", nil), Text: t(locale, "notify.passwordReset.text", map[string]any{"minutes": 30}),
		Severity: plugin.AlertInfo, URL: link,
	}}
	return s.deliver(ctx, mailer.ID, map[string]any{"to": user.Email}, msg)
}
