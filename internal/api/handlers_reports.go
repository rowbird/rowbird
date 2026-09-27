package api

import (
	"context"
	"encoding/json"
	"mime"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/condition"
	"github.com/rowbird/rowbird/internal/reports"
	"github.com/rowbird/rowbird/internal/store"
)

type reportHandlers struct {
	svc *reports.Service
}

func toCondition(s condition.Spec) gen.Condition {
	out := gen.Condition{Match: gen.ConditionMatch(s.Match), Rules: make([]gen.ConditionRule, len(s.Rules))}
	for i, r := range s.Rules {
		props := map[string]any{}
		for k, v := range r.Params {
			props[k] = v
		}
		out.Rules[i] = gen.ConditionRule{Type: r.Type, AdditionalProperties: props}
	}
	return out
}

func fromCondition(c *gen.Condition) condition.Spec {
	if c == nil {
		return condition.Always()
	}
	out := condition.Spec{Match: string(c.Match), Rules: make([]condition.Rule, len(c.Rules))}
	for i, r := range c.Rules {
		params := map[string]any{}
		for k, v := range r.AdditionalProperties {
			params[k] = v
		}
		out.Rules[i] = condition.Rule{Type: r.Type, Params: params}
	}
	return out
}

func toRunSummary(v *reports.RunView) gen.RunSummary {
	r := v.Run
	out := gen.RunSummary{
		Id: r.ID, ReportId: r.ReportID, ReportTitle: v.ReportTitle, Trigger: gen.RunTrigger(r.Trigger), Deliver: r.Deliver,
		Status: gen.RunStatus(r.Status), ScheduledFor: r.ScheduledFor, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		DurationMs: r.DurationMS, RowCount: r.RowCount, Truncated: r.Truncated, Attempt: r.Attempt, ErrorCode: r.ErrorCode,
		CreatedAt: r.CreatedAt,
	}
	if v.TriggeredByName != "" {
		out.TriggeredByName = &v.TriggeredByName
	}
	return out
}

// toRun adds the details. The stored JSON documents already have the API's shape.
func toRun(v *reports.RunView) gen.Run {
	s := toRunSummary(v)
	r := v.Run
	out := gen.Run{
		Id: s.Id, ReportId: s.ReportId, ReportTitle: s.ReportTitle, Trigger: s.Trigger, TriggeredByName: s.TriggeredByName,
		Deliver: s.Deliver, Status: s.Status, ScheduledFor: s.ScheduledFor, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt,
		DurationMs: s.DurationMs, RowCount: s.RowCount, Truncated: s.Truncated, Attempt: s.Attempt, ErrorCode: s.ErrorCode,
		CreatedAt: s.CreatedAt, ErrorMessage: r.ErrorMessage, CancelRequestedAt: r.CancelRequestedAt,
		ResultExpiresAt: r.ResultExpiresAt,
	}
	if v.Version != nil {
		out.Query = &gen.RunQuery{QueryId: v.Version.QueryID, Version: v.Version.Number, Sql: v.Version.SQL}
	}
	decode(r.ResolvedParams, &out.Params)
	decode(r.ConditionResult, &out.Condition)
	decode(r.ResultSample, &out.Sample)
	out.Files = make([]gen.RunFile, 0, len(v.Files))
	for _, f := range v.Files {
		out.Files = append(out.Files, gen.RunFile{Format: f.Format, FileName: f.FileName, ContentType: f.ContentType, SizeBytes: f.SizeBytes, ExpiresAt: f.ExpiresAt})
	}
	out.Deliveries = make([]gen.DeliveryAttempt, 0, len(v.Attempts))
	for _, a := range v.Attempts {
		out.Deliveries = append(out.Deliveries, toDeliveryAttempt(a))
	}
	return out
}

func toDeliveryAttempt(v reports.AttemptView) gen.DeliveryAttempt {
	a := v.Attempt
	meta := a.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	return gen.DeliveryAttempt{
		Id: a.ID, DeliveryId: a.DeliveryID, ChannelId: a.ChannelID, ChannelName: v.ChannelName, ChannelType: v.ChannelType,
		Status: gen.DeliveryAttemptStatus(a.Status), Attempts: a.Attempts, ErrorCode: a.LastErrorCode, ErrorMessage: a.LastError,
		SentAt: a.SentAt, Meta: meta, Resendable: v.Resendable(), CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
}

func decode[T any](raw json.RawMessage, dst **T) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	v := new(T)
	if json.Unmarshal(raw, v) == nil {
		*dst = v
	}
}

func toReportSummary(v *reports.View) gen.ReportSummary {
	rp := v.Report
	out := gen.ReportSummary{
		Id: rp.ID, ManagedBy: gen.ManagedBy(rp.ManagedBy), GitopsOrphan: rp.GitOpsOrphan, Title: rp.Title, Slug: rp.Slug, Description: rp.Description, QueryId: rp.QueryID, QueryTitle: v.QueryTitle,
		Enabled: rp.Enabled, Status: gen.ReportStatus(v.Status), Cron: rp.Cron, Timezone: rp.Timezone, NextRunAt: rp.NextRunAt,
		ConsecutiveFailures: rp.ConsecutiveFailures, UpdatedAt: rp.UpdatedAt,
	}
	if rp.PausedReason != nil {
		reason := gen.PausedReason(*rp.PausedReason)
		out.PausedReason = &reason
	}
	if v.LastRun != nil {
		last := toRunSummary(&reports.RunView{Run: *v.LastRun, ReportTitle: rp.Title})
		out.LastRun = &last
	}
	return out
}

func toReport(v *reports.View) gen.Report {
	s := toReportSummary(v)
	rp := v.Report
	overrides := rp.ParamOverrides
	if overrides == nil {
		overrides = map[string]string{}
	}
	return gen.Report{
		Id: s.Id, ManagedBy: s.ManagedBy, GitopsOrphan: s.GitopsOrphan, Title: s.Title, Slug: s.Slug, Description: s.Description, QueryId: s.QueryId, QueryTitle: s.QueryTitle,
		Enabled: s.Enabled, Status: s.Status, Cron: s.Cron, Timezone: s.Timezone, NextRunAt: s.NextRunAt,
		PausedReason: s.PausedReason, ConsecutiveFailures: s.ConsecutiveFailures, LastRun: s.LastRun, UpdatedAt: s.UpdatedAt,
		Condition: toCondition(v.Condition), ParamOverrides: overrides, MaxRows: rp.MaxRows, RetryMax: rp.RetryMax,
		RetryBackoffSeconds: rp.RetryBackoffSeconds, MisfirePolicy: gen.MisfirePolicy(rp.MisfirePolicy),
		OverlapPolicy: gen.OverlapPolicy(rp.OverlapPolicy), AutoPauseAfter: rp.AutoPauseAfter,
		NotifyOwnerOnFailure: rp.NotifyOwnerOnFail, CreatedAt: rp.CreatedAt, Version: rp.Version,
	}
}

// maxRows maps the API's 0 ("use the connection's limit") to no override.
func maxRows(v *int) *int {
	if v == nil || *v == 0 {
		return nil
	}
	return v
}

func (h *reportHandlers) ListReports(ctx context.Context, _ gen.ListReportsRequestObject) (gen.ListReportsResponseObject, error) {
	list, err := h.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.ListReports200JSONResponse{Items: make([]gen.ReportSummary, len(list))}
	for i := range list {
		out.Items[i] = toReportSummary(&list[i])
	}
	return out, nil
}

func (h *reportHandlers) GetReport(ctx context.Context, req gen.GetReportRequestObject) (gen.GetReportResponseObject, error) {
	v, err := h.svc.Get(ctx, req.ReportId)
	if err != nil {
		return nil, err
	}
	return gen.GetReport200JSONResponse(toReport(v)), nil
}

func (h *reportHandlers) CreateReport(ctx context.Context, req gen.CreateReportRequestObject) (gen.CreateReportResponseObject, error) {
	b := req.Body
	in := reports.Input{
		Title: b.Title, Slug: deref(b.Slug), Description: deref(b.Description), QueryID: b.QueryId, Enabled: b.Enabled,
		Cron: b.Cron, Timezone: deref(b.Timezone), Condition: fromCondition(b.Condition), MaxRows: maxRows(b.MaxRows),
		RetryMax: b.RetryMax, RetryBackoffSeconds: b.RetryBackoffSeconds, AutoPauseAfter: b.AutoPauseAfter,
		NotifyOwnerOnFailure: b.NotifyOwnerOnFailure,
	}
	if b.ParamOverrides != nil {
		in.ParamOverrides = *b.ParamOverrides
	}
	if b.MisfirePolicy != nil {
		in.MisfirePolicy = string(*b.MisfirePolicy)
	}
	if b.OverlapPolicy != nil {
		in.OverlapPolicy = string(*b.OverlapPolicy)
	}
	v, err := h.svc.Create(ctx, PrincipalFrom(ctx), in)
	if err != nil {
		return nil, err
	}
	return gen.CreateReport201JSONResponse(toReport(v)), nil
}

func (h *reportHandlers) UpdateReport(ctx context.Context, req gen.UpdateReportRequestObject) (gen.UpdateReportResponseObject, error) {
	b := req.Body
	patch := reports.Patch{
		Version: b.Version, Title: b.Title, Slug: b.Slug, Description: b.Description, QueryID: b.QueryId, Cron: b.Cron,
		Timezone: b.Timezone, ParamOverrides: b.ParamOverrides, RetryMax: b.RetryMax, RetryBackoffSeconds: b.RetryBackoffSeconds,
		AutoPauseAfter: b.AutoPauseAfter, NotifyOwnerOnFailure: b.NotifyOwnerOnFailure,
	}
	if b.Condition != nil {
		spec := fromCondition(b.Condition)
		patch.Condition = &spec
	}
	if b.MaxRows != nil {
		patch.MaxRows, patch.ClearMaxRows = maxRows(b.MaxRows), *b.MaxRows == 0
	}
	if b.MisfirePolicy != nil {
		p := string(*b.MisfirePolicy)
		patch.MisfirePolicy = &p
	}
	if b.OverlapPolicy != nil {
		p := string(*b.OverlapPolicy)
		patch.OverlapPolicy = &p
	}
	v, err := h.svc.Update(ctx, req.ReportId, patch)
	if err != nil {
		return nil, err
	}
	return gen.UpdateReport200JSONResponse(toReport(v)), nil
}

func (h *reportHandlers) DeleteReport(ctx context.Context, req gen.DeleteReportRequestObject) (gen.DeleteReportResponseObject, error) {
	if err := h.svc.Delete(ctx, req.ReportId); err != nil {
		return nil, err
	}
	return gen.DeleteReport204Response{}, nil
}

func (h *reportHandlers) PauseReport(ctx context.Context, req gen.PauseReportRequestObject) (gen.PauseReportResponseObject, error) {
	v, err := h.svc.Pause(ctx, req.ReportId)
	if err != nil {
		return nil, err
	}
	return gen.PauseReport200JSONResponse(toReport(v)), nil
}

func (h *reportHandlers) ResumeReport(ctx context.Context, req gen.ResumeReportRequestObject) (gen.ResumeReportResponseObject, error) {
	v, err := h.svc.Resume(ctx, req.ReportId)
	if err != nil {
		return nil, err
	}
	return gen.ResumeReport200JSONResponse(toReport(v)), nil
}

func (h *reportHandlers) RunReport(ctx context.Context, req gen.RunReportRequestObject) (gen.RunReportResponseObject, error) {
	in := reports.RunInput{Deliver: true}
	if req.Body != nil && req.Body.Deliver != nil {
		in.Deliver = *req.Body.Deliver
	}
	if req.Params.IdempotencyKey != nil {
		in.IdempotencyKey = *req.Params.IdempotencyKey
	}
	run, created, err := h.svc.Run(ctx, PrincipalFrom(ctx), req.ReportId, in)
	if err != nil {
		return nil, err
	}
	v, err := h.svc.GetRun(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	if !created {
		return gen.RunReport200JSONResponse(toRunSummary(v)), nil
	}
	return gen.RunReport202JSONResponse(toRunSummary(v)), nil
}

func (h *reportHandlers) PreviewSchedule(ctx context.Context, req gen.PreviewScheduleRequestObject) (gen.PreviewScheduleResponseObject, error) {
	b := req.Body
	count := 0
	if b.Count != nil {
		count = *b.Count
	}
	locale := "en"
	if b.Locale != nil {
		locale = *b.Locale
	}
	sv, err := h.svc.PreviewSchedule(ctx, b.Cron, deref(b.Timezone), count, locale)
	if err != nil {
		return nil, err
	}
	return gen.PreviewSchedule200JSONResponse{Expression: sv.Expression, Timezone: sv.Timezone, Description: sv.Description, Next: sv.Next}, nil
}

func (h *reportHandlers) ListRuns(ctx context.Context, req gen.ListRunsRequestObject) (gen.ListRunsResponseObject, error) {
	p := req.Params
	f := store.RunFilter{}
	if p.ReportId != nil {
		f.ReportID = *p.ReportId
	}
	if p.Status != nil {
		for _, s := range *p.Status {
			if !s.Valid() {
				return nil, apperr.Invalid(apperr.Field("status", "validation.invalid_value"))
			}
			f.Statuses = append(f.Statuses, string(s))
		}
	}
	if p.Trigger != nil {
		if !p.Trigger.Valid() {
			return nil, apperr.Invalid(apperr.Field("trigger", "validation.invalid_value"))
		}
		f.Trigger = string(*p.Trigger)
	}
	if p.From != nil {
		f.From = *p.From
	}
	if p.To != nil {
		f.To = *p.To
	}
	page, err := h.svc.ListRuns(ctx, f, pageRequest(p.Limit, p.Cursor))
	if err != nil {
		return nil, err
	}
	out := gen.ListRuns200JSONResponse{Items: make([]gen.RunSummary, len(page.Items)), NextCursor: nextCursor(page.NextCursor)}
	for i := range page.Items {
		out.Items[i] = toRunSummary(&page.Items[i])
	}
	return out, nil
}

func (h *reportHandlers) GetRun(ctx context.Context, req gen.GetRunRequestObject) (gen.GetRunResponseObject, error) {
	v, err := h.svc.GetRun(ctx, req.RunId)
	if err != nil {
		return nil, err
	}
	return gen.GetRun200JSONResponse(toRun(v)), nil
}

func (h *reportHandlers) CancelRun(ctx context.Context, req gen.CancelRunRequestObject) (gen.CancelRunResponseObject, error) {
	v, err := h.svc.CancelRun(ctx, req.RunId)
	if err != nil {
		return nil, err
	}
	return gen.CancelRun200JSONResponse(toRun(v)), nil
}

func (h *reportHandlers) DownloadRunResult(ctx context.Context, req gen.DownloadRunResultRequestObject) (gen.DownloadRunResultResponseObject, error) {
	format := ""
	if req.Params.Format != nil {
		format = *req.Params.Format
	}
	f, err := h.svc.RunResult(ctx, PrincipalFrom(ctx), req.RunId, format)
	if err != nil {
		return nil, err
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": f.Name})
	return gen.DownloadRunResult200ApplicationoctetStreamResponse{
		Body: f.Body, ContentLength: f.Size, Headers: gen.DownloadRunResult200ResponseHeaders{ContentDisposition: &disposition},
	}, nil
}

func toDelivery(v *reports.DeliveryView) gen.Delivery {
	d := v.Delivery
	opts := d.Options
	if opts == nil {
		opts = map[string]any{}
	}
	formats := d.Formats
	if formats == nil {
		formats = []string{}
	}
	return gen.Delivery{
		Id: d.ID, ReportId: d.ReportID, ChannelId: d.ChannelID, ChannelName: v.ChannelName, ChannelType: v.ChannelType,
		Position: d.Position, Enabled: d.Enabled, Mode: gen.DeliveryMode(d.Mode), Formats: formats, InlineRowLimit: d.InlineRowLimit,
		IncludeInlineWithFiles: d.IncludeInlineWithFiles, LinkExpiresSeconds: d.LinkExpiresSeconds, LinkRequireLogin: d.LinkRequireLogin,
		Options: opts, Version: d.Version,
	}
}

func deliveryInput(channel openapi_types.UUID, enabled *bool, mode *gen.DeliveryMode, formats *[]string, rows *int, inline *bool, expires *int, login *bool, opts *map[string]any) reports.DeliveryInput {
	in := reports.DeliveryInput{ChannelID: channel, Enabled: enabled, InlineRowLimit: rows, IncludeInlineWithFiles: inline, LinkExpiresSeconds: expires, LinkRequireLogin: login}
	if mode != nil {
		in.Mode = string(*mode)
	}
	if formats != nil {
		in.Formats = *formats
	}
	if opts != nil {
		in.Options = *opts
	}
	return in
}

func (h *reportHandlers) ListDeliveries(ctx context.Context, req gen.ListDeliveriesRequestObject) (gen.ListDeliveriesResponseObject, error) {
	list, err := h.svc.Deliveries(ctx, req.ReportId)
	if err != nil {
		return nil, err
	}
	out := gen.ListDeliveries200JSONResponse{Items: make([]gen.Delivery, len(list))}
	for i := range list {
		out.Items[i] = toDelivery(&list[i])
	}
	return out, nil
}

func (h *reportHandlers) CreateDelivery(ctx context.Context, req gen.CreateDeliveryRequestObject) (gen.CreateDeliveryResponseObject, error) {
	b := req.Body
	v, err := h.svc.CreateDelivery(ctx, req.ReportId, deliveryInput(b.ChannelId, b.Enabled, b.Mode, b.Formats, b.InlineRowLimit, b.IncludeInlineWithFiles, b.LinkExpiresSeconds, b.LinkRequireLogin, b.Options))
	if err != nil {
		return nil, err
	}
	return gen.CreateDelivery201JSONResponse(toDelivery(v)), nil
}

func (h *reportHandlers) UpdateDelivery(ctx context.Context, req gen.UpdateDeliveryRequestObject) (gen.UpdateDeliveryResponseObject, error) {
	b := req.Body
	v, err := h.svc.UpdateDelivery(ctx, req.ReportId, req.DeliveryId, b.Version,
		deliveryInput(b.ChannelId, b.Enabled, b.Mode, b.Formats, b.InlineRowLimit, b.IncludeInlineWithFiles, b.LinkExpiresSeconds, b.LinkRequireLogin, b.Options))
	if err != nil {
		return nil, err
	}
	return gen.UpdateDelivery200JSONResponse(toDelivery(v)), nil
}

func (h *reportHandlers) DeleteDelivery(ctx context.Context, req gen.DeleteDeliveryRequestObject) (gen.DeleteDeliveryResponseObject, error) {
	if err := h.svc.DeleteDelivery(ctx, req.ReportId, req.DeliveryId); err != nil {
		return nil, err
	}
	return gen.DeleteDelivery204Response{}, nil
}

func (h *reportHandlers) PreviewDelivery(ctx context.Context, req gen.PreviewDeliveryRequestObject) (gen.PreviewDeliveryResponseObject, error) {
	b := req.Body
	p, err := h.svc.PreviewDelivery(ctx, b.ReportId, deliveryInput(b.ChannelId, b.Enabled, b.Mode, b.Formats, b.InlineRowLimit, b.IncludeInlineWithFiles, b.LinkExpiresSeconds, b.LinkRequireLogin, b.Options))
	if err != nil {
		return nil, err
	}
	out := gen.PreviewDelivery200JSONResponse{Body: p.Body, BodyType: gen.DeliveryPreviewBodyType(p.BodyType), Attachments: p.Attachments, Links: p.Links}
	if out.Attachments == nil {
		out.Attachments = []string{}
	}
	if out.Links == nil {
		out.Links = []string{}
	}
	if p.Subject != "" {
		out.Subject = &p.Subject
	}
	return out, nil
}

func (h *reportHandlers) TestDelivery(ctx context.Context, req gen.TestDeliveryRequestObject) (gen.TestDeliveryResponseObject, error) {
	var channel *openapi_types.UUID
	if req.Body != nil {
		channel = req.Body.ChannelId
	}
	run, err := h.svc.TestDelivery(ctx, PrincipalFrom(ctx), req.ReportId, channel)
	if err != nil {
		return nil, err
	}
	v, err := h.svc.GetRun(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	return gen.TestDelivery202JSONResponse(toRunSummary(v)), nil
}

func (h *reportHandlers) RetryAttempt(ctx context.Context, req gen.RetryAttemptRequestObject) (gen.RetryAttemptResponseObject, error) {
	v, err := h.svc.RetryAttempt(ctx, req.RunId, req.AttemptId)
	if err != nil {
		return nil, err
	}
	return gen.RetryAttempt200JSONResponse(toDeliveryAttempt(*v)), nil
}

func toDashboardReport(rp *store.Report) gen.DashboardReport {
	return gen.DashboardReport{
		Id: rp.ID, Title: rp.Title, NextRunAt: rp.NextRunAt, Timezone: rp.Timezone,
		ConsecutiveFailures: rp.ConsecutiveFailures, PausedReason: rp.PausedReason,
	}
}

func (h *reportHandlers) GetDashboard(ctx context.Context, _ gen.GetDashboardRequestObject) (gen.GetDashboardResponseObject, error) {
	d, err := h.svc.Dashboard(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.GetDashboard200JSONResponse{
		NextRuns: []gen.DashboardReport{}, RecentFailures: []gen.RunSummary{}, FailingChannels: []gen.DashboardChannel{}, FailingReports: []gen.DashboardReport{},
		SuccessRate7d:  gen.SuccessRate{Succeeded: d.Rate7.Succeeded, Total: d.Rate7.Total},
		SuccessRate30d: gen.SuccessRate{Succeeded: d.Rate30.Succeeded, Total: d.Rate30.Total},
		Onboarding: gen.Onboarding{
			HasConnection: d.Onboarding.HasConnection, HasQuery: d.Onboarding.HasQuery,
			HasReport: d.Onboarding.HasReport, HasDelivery: d.Onboarding.HasDelivery,
		},
	}
	for i := range d.NextRuns {
		out.NextRuns = append(out.NextRuns, toDashboardReport(&d.NextRuns[i]))
	}
	for i := range d.FailingReports {
		out.FailingReports = append(out.FailingReports, toDashboardReport(&d.FailingReports[i]))
	}
	for i := range d.RecentFailures {
		out.RecentFailures = append(out.RecentFailures, toRunSummary(&d.RecentFailures[i]))
	}
	for _, c := range d.FailingChannels {
		out.FailingChannels = append(out.FailingChannels, gen.DashboardChannel{Id: c.ID, Name: c.Name, Type: c.Type, LastError: c.LastError, LastFailureAt: c.LastFailureAt})
	}
	return out, nil
}
