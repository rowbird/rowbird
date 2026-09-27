// Package reports manages reports (a query on a schedule, with a condition) and their runs
// (docs/spec/03-flows.md, sections 4 to 7). Executing runs is the runner's job; this package
// creates them, lists them and cancels them.
package reports

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/condition"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/schedule"
	"github.com/rowbird/rowbird/internal/slug"
	"github.com/rowbird/rowbird/internal/store"
)

// Defaults and limits (docs/spec/02-data-model.md).
const (
	DefaultRetryMax        = 2
	DefaultRetryBackoff    = 30
	DefaultAutoPauseAfter  = 5
	MaxRetries             = 10
	MaxRetryBackoffSeconds = 3600
	MaxAutoPauseAfter      = 100
	IdempotencyWindow      = 24 * time.Hour
	MaxScheduleOccurrences = 20
)

// Run error codes set by this package.
const (
	CodeCancelled = "run.cancelled"
)

// Errors.
var (
	ErrSlugTaken    = apperr.New(apperr.KindConflict, "report.slug_taken")
	ErrRunActive    = apperr.New(apperr.KindConflict, "report.run_active")
	ErrRunNotActive = apperr.New(apperr.KindConflict, "run.not_active")
)

// Service manages reports and runs.
type Service struct {
	store     *store.Store
	queries   *queries.Service
	logger    *slog.Logger
	now       func() time.Time
	wake      func()
	cancel    func(runID uuid.UUID)
	spoolDir  string
	baseURL   string
	deliverer Deliverer
	storage   plugin.Storage
	// background runs bulk resends after the request returns.
	background func(func())
	notifier   Notifier
}

// Notifier is told when reports and runs change, for the UI and for notifications.
type Notifier interface {
	RunUpdated(ctx context.Context, run *store.Run)
	ReportUpdated(ctx context.Context, reportID uuid.UUID)
	ReportResumed(ctx context.Context, reportID uuid.UUID)
}

type noNotifier struct{}

func (noNotifier) RunUpdated(context.Context, *store.Run)   {}
func (noNotifier) ReportUpdated(context.Context, uuid.UUID) {}
func (noNotifier) ReportResumed(context.Context, uuid.UUID) {}

// Options configure the service.
type Options struct {
	Logger *slog.Logger
	Now    func() time.Time
	// Wake tells local workers that a run was queued, so they do not wait for the next tick.
	Wake func()
	// CancelLocal stops a run executing in this process right away. Runs on other instances see
	// the request on their next tick.
	CancelLocal func(runID uuid.UUID)
	// SpoolDir is where runs keep their results for downloads.
	SpoolDir string
	// BaseURL is the public address; without it deliveries cannot use links.
	BaseURL string
	// Deliverer previews and resends deliveries (the delivery engine).
	Deliverer Deliverer
	// Storage holds the files deliveries generated, for run downloads.
	Storage plugin.Storage
	// Background runs work after a request returns (bulk resends); tests make it synchronous.
	Background func(func())
	// Notifier learns about report and run changes; nil tells nobody.
	Notifier Notifier
}

// NewService builds the service.
func NewService(st *store.Store, q *queries.Service, opts Options) *Service {
	s := &Service{store: st, queries: q, logger: opts.Logger, now: opts.Now, wake: opts.Wake, cancel: opts.CancelLocal, spoolDir: opts.SpoolDir, baseURL: opts.BaseURL, deliverer: opts.Deliverer, background: opts.Background, storage: opts.Storage, notifier: opts.Notifier}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.wake == nil {
		s.wake = func() {}
	}
	if s.background == nil {
		s.background = func(f func()) { go f() }
	}
	if s.cancel == nil {
		s.cancel = func(uuid.UUID) {}
	}
	if s.notifier == nil {
		s.notifier = noNotifier{}
	}
	return s
}

func (s *Service) clock() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

// Report statuses shown in lists.
const (
	StatusActive  = "active"
	StatusPaused  = "paused"
	StatusFailing = "failing"
)

// View is a report with what the UI shows around it.
type View struct {
	Report     store.Report
	Condition  condition.Spec
	QueryTitle string
	LastRun    *store.Run
	Status     string
}

// Input creates a report. Zero policies take their defaults; an empty Timezone takes the
// workspace's default; an empty Slug is derived from Title.
type Input struct {
	Title                string
	Slug                 string
	Description          string
	QueryID              uuid.UUID
	Enabled              *bool
	Cron                 string
	Timezone             string
	Condition            condition.Spec
	ParamOverrides       map[string]string
	MaxRows              *int
	RetryMax             *int
	RetryBackoffSeconds  *int
	MisfirePolicy        string
	OverlapPolicy        string
	AutoPauseAfter       *int
	NotifyOwnerOnFailure *bool
}

// Patch changes a report. Nil fields are left alone. MaxRows uses ClearMaxRows to remove it.
type Patch struct {
	Version              int64
	Title                *string
	Slug                 *string
	Description          *string
	QueryID              *uuid.UUID
	Cron                 *string
	Timezone             *string
	Condition            *condition.Spec
	ParamOverrides       *map[string]string
	MaxRows              *int
	ClearMaxRows         bool
	RetryMax             *int
	RetryBackoffSeconds  *int
	MisfirePolicy        *string
	OverlapPolicy        *string
	AutoPauseAfter       *int
	NotifyOwnerOnFailure *bool
}

// List returns every report with its last run.
func (s *Service) List(ctx context.Context) ([]View, error) {
	rps, err := s.store.Reports().List(ctx)
	if err != nil {
		return nil, err
	}
	latest, err := s.store.Runs().Latest(ctx)
	if err != nil {
		return nil, err
	}
	qs, err := s.store.Queries().List(ctx)
	if err != nil {
		return nil, err
	}
	titles := make(map[uuid.UUID]string, len(qs))
	for _, q := range qs {
		titles[q.ID] = q.Title
	}
	out := make([]View, 0, len(rps))
	for _, rp := range rps {
		v := View{Report: rp, QueryTitle: titles[rp.QueryID]}
		if run, ok := latest[rp.ID]; ok {
			v.LastRun = &run
		}
		out = append(out, s.finishView(v))
	}
	return out, nil
}

// Get returns one report.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*View, error) {
	rp, err := s.store.Reports().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	v := View{Report: *rp}
	if q, err := s.store.Queries().Get(ctx, rp.QueryID); err == nil {
		v.QueryTitle = q.Title
	}
	page, err := s.store.Runs().List(ctx, store.RunFilter{ReportID: id}, store.PageRequest{Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(page.Items) > 0 {
		v.LastRun = &page.Items[0]
	}
	out := s.finishView(v)
	return &out, nil
}

func (s *Service) finishView(v View) View {
	if err := json.Unmarshal(v.Report.Condition, &v.Condition); err != nil || v.Condition.Rules == nil {
		v.Condition = condition.Always()
	}
	switch {
	case !v.Report.Enabled:
		v.Status = StatusPaused
	case v.Report.ConsecutiveFailures > 0 || (v.LastRun != nil && v.LastRun.Status == store.RunFailed):
		v.Status = StatusFailing
	default:
		v.Status = StatusActive
	}
	return v
}

// Create stores a report and schedules its first run.
func (s *Service) Create(ctx context.Context, p *auth.Principal, in Input) (*View, error) {
	if in.Slug == "" {
		in.Slug = slug.Make(in.Title, "report")
	}
	if in.Timezone == "" {
		in.Timezone = s.defaultTimezone(ctx)
	}
	rp := &store.Report{
		Title: strings.TrimSpace(in.Title), Slug: in.Slug, Description: strings.TrimSpace(in.Description),
		QueryID: in.QueryID, Enabled: in.Enabled == nil || *in.Enabled, Cron: in.Cron, Timezone: in.Timezone,
		ParamOverrides: in.ParamOverrides, MaxRows: in.MaxRows,
		RetryMax: DefaultRetryMax, RetryBackoffSeconds: DefaultRetryBackoff, AutoPauseAfter: DefaultAutoPauseAfter,
		MisfirePolicy: store.MisfireRunOnce, OverlapPolicy: store.OverlapSkip, NotifyOwnerOnFail: true, OwnerID: &p.UserID,
	}
	rp.CreatedBy = &p.UserID
	setInt(&rp.RetryMax, in.RetryMax)
	setInt(&rp.RetryBackoffSeconds, in.RetryBackoffSeconds)
	setInt(&rp.AutoPauseAfter, in.AutoPauseAfter)
	if in.MisfirePolicy != "" {
		rp.MisfirePolicy = in.MisfirePolicy
	}
	if in.OverlapPolicy != "" {
		rp.OverlapPolicy = in.OverlapPolicy
	}
	if in.NotifyOwnerOnFailure != nil {
		rp.NotifyOwnerOnFail = *in.NotifyOwnerOnFailure
	}
	if rp.ParamOverrides == nil {
		rp.ParamOverrides = map[string]string{}
	}
	sched, cond, err := s.validate(ctx, rp, in.Condition)
	if err != nil {
		return nil, err
	}
	rp.Condition = cond
	s.reschedule(rp, sched)
	if err := s.store.Reports().Create(ctx, rp); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return nil, ErrSlugTaken
		}
		return nil, err
	}
	return s.Get(ctx, rp.ID)
}

func setInt(dst, v *int) {
	if v != nil {
		*dst = *v
	}
}

// Update applies a patch with optimistic concurrency. A change to the schedule recomputes the next
// run from now.
func (s *Service) Update(ctx context.Context, id uuid.UUID, patch Patch) (*View, error) {
	rp, err := s.store.Reports().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := store.CheckManaged(ctx, rp.ManagedBy); err != nil {
		return nil, err
	}
	rp.Version = patch.Version
	var cols []string
	set := func(col string, apply func()) {
		apply()
		cols = append(cols, col)
	}
	if patch.Title != nil {
		set("title", func() { rp.Title = strings.TrimSpace(*patch.Title) })
	}
	if patch.Slug != nil {
		set("slug", func() { rp.Slug = *patch.Slug })
	}
	if patch.Description != nil {
		set("description", func() { rp.Description = strings.TrimSpace(*patch.Description) })
	}
	if patch.QueryID != nil {
		set("query_id", func() { rp.QueryID = *patch.QueryID })
	}
	if patch.Cron != nil {
		set("cron", func() { rp.Cron = *patch.Cron })
	}
	if patch.Timezone != nil {
		set("timezone", func() { rp.Timezone = *patch.Timezone })
	}
	if patch.ParamOverrides != nil {
		set("param_overrides", func() { rp.ParamOverrides = *patch.ParamOverrides })
	}
	if patch.MaxRows != nil || patch.ClearMaxRows {
		set("max_rows", func() { rp.MaxRows = patch.MaxRows })
	}
	if patch.RetryMax != nil {
		set("retry_max", func() { rp.RetryMax = *patch.RetryMax })
	}
	if patch.RetryBackoffSeconds != nil {
		set("retry_backoff_seconds", func() { rp.RetryBackoffSeconds = *patch.RetryBackoffSeconds })
	}
	if patch.MisfirePolicy != nil {
		set("misfire_policy", func() { rp.MisfirePolicy = *patch.MisfirePolicy })
	}
	if patch.OverlapPolicy != nil {
		set("overlap_policy", func() { rp.OverlapPolicy = *patch.OverlapPolicy })
	}
	if patch.AutoPauseAfter != nil {
		set("auto_pause_after", func() { rp.AutoPauseAfter = *patch.AutoPauseAfter })
	}
	if patch.NotifyOwnerOnFailure != nil {
		set("notify_owner_on_failure", func() { rp.NotifyOwnerOnFail = *patch.NotifyOwnerOnFailure })
	}
	var spec condition.Spec
	if err := json.Unmarshal(rp.Condition, &spec); err != nil {
		spec = condition.Always()
	}
	if patch.Condition != nil {
		spec = *patch.Condition
		cols = append(cols, "condition")
	}
	if rp.ParamOverrides == nil {
		rp.ParamOverrides = map[string]string{}
	}
	sched, cond, err := s.validate(ctx, rp, spec)
	if err != nil {
		return nil, err
	}
	rp.Condition = cond
	if patch.Cron != nil || patch.Timezone != nil {
		s.reschedule(rp, sched)
		cols = append(cols, "next_run_at")
	}
	if len(cols) > 0 {
		if err := s.store.Reports().Update(ctx, rp, cols...); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return nil, ErrSlugTaken
			}
			return nil, err
		}
	}
	return s.Get(ctx, id)
}

// reschedule sets next_run_at from now for an enabled report.
func (s *Service) reschedule(rp *store.Report, sched *schedule.Schedule) {
	rp.NextRunAt = nil
	if rp.Enabled {
		if next := sched.Next(s.clock()); !next.IsZero() {
			rp.NextRunAt = &next
		}
	}
}

// validate checks everything about a report and returns its parsed schedule and normalized
// condition JSON.
func (s *Service) validate(ctx context.Context, rp *store.Report, spec condition.Spec) (*schedule.Schedule, json.RawMessage, error) {
	fe := slug.CheckMeta(rp.Title, rp.Slug, rp.Description)
	sched, err := schedule.Parse(rp.Cron, rp.Timezone)
	switch {
	case errors.Is(err, schedule.ErrInvalidTimezone):
		fe = append(fe, apperr.Field("timezone", "validation.timezone"))
	case errors.Is(err, schedule.ErrNeverRuns):
		fe = append(fe, apperr.Field("cron", "validation.cron_never"))
	case err != nil:
		fe = append(fe, apperr.Field("cron", "validation.cron"))
	}
	if err == nil {
		rp.Cron = sched.Expression()
	}
	if _, err := s.store.Queries().Get(ctx, rp.QueryID); err != nil {
		fe = append(fe, apperr.Field("query_id", "validation.invalid_value"))
	} else {
		pfe, err := s.queries.CheckValues(ctx, rp.QueryID, rp.ParamOverrides, "param_overrides")
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, nil, err
		}
		fe = append(fe, pfe...)
	}
	fe = append(fe, checkRange("max_rows", rp.MaxRows, 1, 1<<31-1)...)
	fe = append(fe, checkRange("retry_max", &rp.RetryMax, 0, MaxRetries)...)
	fe = append(fe, checkRange("retry_backoff_seconds", &rp.RetryBackoffSeconds, 1, MaxRetryBackoffSeconds)...)
	fe = append(fe, checkRange("auto_pause_after", &rp.AutoPauseAfter, 0, MaxAutoPauseAfter)...)
	if rp.MisfirePolicy != store.MisfireRunOnce && rp.MisfirePolicy != store.MisfireSkip {
		fe = append(fe, apperr.Field("misfire_policy", "validation.invalid_value"))
	}
	if rp.OverlapPolicy != store.OverlapSkip && rp.OverlapPolicy != store.OverlapQueue {
		fe = append(fe, apperr.Field("overlap_policy", "validation.invalid_value"))
	}
	normalized, err := condition.Validate(spec, "condition")
	if ae, ok := apperr.As(err); ok {
		fe = append(fe, ae.Fields...)
	} else if err != nil {
		return nil, nil, err
	}
	if len(fe) > 0 {
		return nil, nil, apperr.Invalid(fe...)
	}
	cond, err := json.Marshal(normalized)
	if err != nil {
		return nil, nil, err
	}
	return sched, cond, nil
}

func checkRange(field string, v *int, lo, hi int) []apperr.FieldError {
	if v != nil && (*v < lo || *v > hi) {
		return []apperr.FieldError{apperr.Field(field, "validation.range")}
	}
	return nil
}

// Delete removes a report and its runs. A report with a run in progress cannot be deleted.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if rp, err := s.store.Reports().Get(ctx, id); err == nil {
		if err := store.CheckManaged(ctx, rp.ManagedBy); err != nil {
			return err
		}
	}
	n, err := s.store.Runs().CountActive(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrRunActive
	}
	return s.store.Reports().Delete(ctx, id)
}

// Pause stops scheduling a report.
func (s *Service) Pause(ctx context.Context, id uuid.UUID) (*View, error) {
	return s.PauseWithReason(ctx, id, store.PausedManual)
}

// PauseWithReason stops scheduling a report and records why (store.Paused*).
func (s *Service) PauseWithReason(ctx context.Context, id uuid.UUID, reason string) (*View, error) {
	rp, err := s.store.Reports().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if rp.Enabled {
		rp.Enabled, rp.PausedReason, rp.NextRunAt = false, &reason, nil
		if err := s.store.Reports().Update(ctx, rp, "enabled", "paused_reason", "next_run_at"); err != nil {
			return nil, err
		}
		s.notifier.ReportUpdated(ctx, id)
	}
	return s.Get(ctx, id)
}

// Resume schedules a paused report again from now and clears its failure streak.
func (s *Service) Resume(ctx context.Context, id uuid.UUID) (*View, error) {
	rp, err := s.store.Reports().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !rp.Enabled {
		sched, err := schedule.Parse(rp.Cron, rp.Timezone)
		if err != nil {
			return nil, apperr.Invalid(apperr.Field("cron", "validation.cron"))
		}
		rp.Enabled, rp.PausedReason, rp.ConsecutiveFailures = true, nil, 0
		s.reschedule(rp, sched)
		if err := s.store.Reports().Update(ctx, rp, "enabled", "paused_reason", "consecutive_failures", "next_run_at"); err != nil {
			return nil, err
		}
		s.notifier.ReportResumed(ctx, id)
	}
	return s.Get(ctx, id)
}

// RunInput starts a manual run.
type RunInput struct {
	// Deliver false only shows the result: nothing is delivered and the run never counts toward
	// failures or the "changed" condition.
	Deliver        bool
	IdempotencyKey string
}

// Run queues a manual run. With an idempotency key already used for this report in the last 24
// hours it returns that run instead, and created is false. Paused reports can be run manually.
func (s *Service) Run(ctx context.Context, p *auth.Principal, id uuid.UUID, in RunInput) (run *store.Run, created bool, err error) {
	if _, err := s.store.Reports().Get(ctx, id); err != nil {
		return nil, false, err
	}
	if len(in.IdempotencyKey) > 255 {
		return nil, false, apperr.Invalid(apperr.Field("idempotency_key", "validation.too_long"))
	}
	at := s.clock()
	if in.IdempotencyKey != "" {
		prev, err := s.store.Runs().ByIdempotencyKey(ctx, id, in.IdempotencyKey, at.Add(-IdempotencyWindow))
		if err == nil {
			return prev, false, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, false, err
		}
	}
	run = &store.Run{ReportID: id, Trigger: store.TriggerManual, TriggeredBy: &p.UserID, Deliver: in.Deliver, AvailableAt: at}
	run.CreatedBy, run.CreatedAt = &p.UserID, at
	if in.IdempotencyKey != "" {
		run.IdempotencyKey = &in.IdempotencyKey
	}
	if err := s.store.Runs().Create(ctx, run); err != nil {
		return nil, false, err
	}
	s.logger.InfoContext(ctx, "manual run queued", "run_id", run.ID, "report_id", id, "deliver", in.Deliver)
	s.notifier.RunUpdated(ctx, run)
	s.wake()
	return run, true, nil
}

// ScheduleView previews a schedule.
type ScheduleView struct {
	Expression  string
	Timezone    string
	Description string
	Next        []time.Time
}

// PreviewSchedule returns the next occurrences of a schedule and its description in locale.
func (s *Service) PreviewSchedule(ctx context.Context, cron, timezone string, count int, locale string) (*ScheduleView, error) {
	if timezone == "" {
		timezone = s.defaultTimezone(ctx)
	}
	if count == 0 {
		count = 5
	}
	var fe []apperr.FieldError
	if count < 1 || count > MaxScheduleOccurrences {
		fe = append(fe, apperr.Field("count", "validation.range"))
	}
	sched, err := schedule.Parse(cron, timezone)
	switch {
	case errors.Is(err, schedule.ErrInvalidTimezone):
		fe = append(fe, apperr.Field("timezone", "validation.timezone"))
	case errors.Is(err, schedule.ErrNeverRuns):
		fe = append(fe, apperr.Field("cron", "validation.cron_never"))
	case err != nil:
		fe = append(fe, apperr.Field("cron", "validation.cron"))
	}
	if len(fe) > 0 {
		return nil, apperr.Invalid(fe...)
	}
	return &ScheduleView{
		Expression: sched.Expression(), Timezone: timezone,
		Description: schedule.Describe(sched.Expression(), locale),
		Next:        sched.NextN(s.clock(), count),
	}, nil
}

func (s *Service) defaultTimezone(ctx context.Context) string {
	st, err := s.store.Settings().Get(ctx, auth.SettingDefaultTimezone)
	if err != nil {
		return "UTC"
	}
	var tz string
	if json.Unmarshal([]byte(st.Value), &tz) != nil || tz == "" {
		return "UTC"
	}
	return tz
}

// LinksEnabled reports whether deliveries can share links (ROWBIRD_BASE_URL is set).
func (s *Service) LinksEnabled() bool { return s.baseURL != "" }
