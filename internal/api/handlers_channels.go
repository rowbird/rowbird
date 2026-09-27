package api

import (
	"context"
	"math"
	"time"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/reports"
	"github.com/rowbird/rowbird/internal/store"
)

type channelHandlers struct {
	svc     *channels.Service
	reports *reports.Service
	auth    *auth.Service
}

func toChannel(v *channels.View) gen.Channel {
	cfg := map[string]any{}
	for k, val := range v.Config {
		cfg[k] = val
	}
	for k, set := range v.SecretsConfigured {
		cfg[k] = map[string]any{"configured": set}
	}
	used := make([]gen.Dependent, len(v.UsedBy))
	for i, d := range v.UsedBy {
		used[i] = gen.Dependent{Type: d.Type, Id: d.ID, Name: d.Name}
	}
	caps := map[string]any{}
	if p, ok := plugin.Get(plugin.KindDestination, v.Type); ok {
		if d, ok := p.(plugin.Destination); ok {
			caps, _ = toObject(plugin.CapabilitiesOf(d, wholeNumbers(v.Config)))
		}
	}
	return gen.Channel{
		Id: v.ID, Name: v.Name, ManagedBy: gen.ManagedBy(v.ManagedBy), Type: v.Type, Config: cfg, Capabilities: caps, Status: gen.ChannelStatus(v.Status),
		LastSuccessAt: v.LastSuccessAt, LastFailureAt: v.LastFailureAt, LastError: v.LastError,
		IsSystemMailer: v.IsSystemMailer, UsedBy: used, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Version: v.Version,
	}
}

// wholeNumbers turns whole float64 values (numbers read back from JSON) into int64, the type
// destinations read integer settings as.
func wholeNumbers(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if f, ok := v.(float64); ok && f == math.Trunc(f) {
			out[k] = int64(f)
			continue
		}
		out[k] = v
	}
	return out
}

func toChannelTestResult(r *channels.TestResult) gen.ChannelTestResult {
	out := gen.ChannelTestResult{Ok: r.OK}
	if r.ErrorCode != "" {
		out.ErrorCode, out.ErrorMessage = &r.ErrorCode, &r.ErrorMessage
	}
	return out
}

func (h *channelHandlers) ListChannels(ctx context.Context, _ gen.ListChannelsRequestObject) (gen.ListChannelsResponseObject, error) {
	list, err := h.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.ListChannels200JSONResponse{Items: make([]gen.Channel, len(list))}
	for i := range list {
		out.Items[i] = toChannel(&list[i])
	}
	return out, nil
}

func (h *channelHandlers) GetChannel(ctx context.Context, req gen.GetChannelRequestObject) (gen.GetChannelResponseObject, error) {
	v, err := h.svc.Get(ctx, req.ChannelId)
	if err != nil {
		return nil, err
	}
	return gen.GetChannel200JSONResponse(toChannel(v)), nil
}

func (h *channelHandlers) CreateChannel(ctx context.Context, req gen.CreateChannelRequestObject) (gen.CreateChannelResponseObject, error) {
	b := req.Body
	in := channels.Input{Name: b.Name, Type: b.Type, Config: b.Config}
	if b.IsSystemMailer != nil {
		in.IsSystemMailer = *b.IsSystemMailer
	}
	v, err := h.svc.Create(ctx, PrincipalFrom(ctx), in, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.CreateChannel201JSONResponse(toChannel(v)), nil
}

func (h *channelHandlers) UpdateChannel(ctx context.Context, req gen.UpdateChannelRequestObject) (gen.UpdateChannelResponseObject, error) {
	b := req.Body
	patch := channels.Patch{Version: b.Version, Name: b.Name, IsSystemMailer: b.IsSystemMailer}
	if b.Config != nil {
		patch.Config = *b.Config
	}
	v, err := h.svc.Update(ctx, PrincipalFrom(ctx), req.ChannelId, patch, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.UpdateChannel200JSONResponse(toChannel(v)), nil
}

func (h *channelHandlers) DeleteChannel(ctx context.Context, req gen.DeleteChannelRequestObject) (gen.DeleteChannelResponseObject, error) {
	if err := h.svc.Delete(ctx, PrincipalFrom(ctx), req.ChannelId, requestMetaFrom(ctx)); err != nil {
		return nil, err
	}
	return gen.DeleteChannel204Response{}, nil
}

func (h *channelHandlers) TestChannelConfig(ctx context.Context, req gen.TestChannelConfigRequestObject) (gen.TestChannelConfigResponseObject, error) {
	b := req.Body
	res, err := h.svc.Test(ctx, channels.TestInput{ChannelID: b.ChannelId, Type: b.Type, Config: b.Config, To: deref(b.To), Locale: h.locale(ctx)})
	if err != nil {
		return nil, err
	}
	return gen.TestChannelConfig200JSONResponse(toChannelTestResult(res)), nil
}

func (h *channelHandlers) TestChannel(ctx context.Context, req gen.TestChannelRequestObject) (gen.TestChannelResponseObject, error) {
	to := ""
	if req.Body != nil {
		to = deref(req.Body.To)
	}
	if to == "" {
		to = h.email(ctx)
	}
	res, err := h.svc.TestSaved(ctx, req.ChannelId, to, h.locale(ctx))
	if err != nil {
		return nil, err
	}
	return gen.TestChannel200JSONResponse(toChannelTestResult(res)), nil
}

func (h *channelHandlers) user(ctx context.Context) *store.User {
	p := PrincipalFrom(ctx)
	if p == nil || h.auth == nil {
		return nil
	}
	u, err := h.auth.Me(ctx, p)
	if err != nil {
		return nil
	}
	return u
}

func (h *channelHandlers) locale(ctx context.Context) string {
	if u := h.user(ctx); u != nil && u.Locale != "" {
		return u.Locale
	}
	return "en"
}

func (h *channelHandlers) email(ctx context.Context) string {
	if u := h.user(ctx); u != nil {
		return u.Email
	}
	return ""
}

func (h *channelHandlers) RetryFailedDeliveries(ctx context.Context, req gen.RetryFailedDeliveriesRequestObject) (gen.RetryFailedDeliveriesResponseObject, error) {
	var since time.Time
	if req.Body != nil && req.Body.Since != nil {
		since = *req.Body.Since
	}
	n, err := h.reports.RetryFailed(ctx, req.ChannelId, since)
	if err != nil {
		return nil, err
	}
	return gen.RetryFailedDeliveries202JSONResponse{Queued: n}, nil
}
