package api

import (
	"context"
	"time"

	"github.com/rowbird/rowbird/internal/ai"
	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
)

// AI requests allowed per user (docs/spec/05-api.md, "Rate limits").
const (
	aiBurst  = 20
	aiWindow = 10 * time.Minute
)

// CodeAITooManyRequests is the per-user limit of the assistant (the provider's own limit is
// ai.rate_limited).
const CodeAITooManyRequests = "ai.too_many_requests"

type aiHandlers struct {
	svc     *ai.Service
	auth    *auth.Service
	limiter *rateLimiter
}

func toAISettings(s ai.Settings) gen.AISettings {
	cfg := s.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	return gen.AISettings{Provider: s.Provider, Config: cfg}
}

func aiInput(b gen.AISettingsInput) ai.SettingsInput {
	in := ai.SettingsInput{Provider: b.Provider}
	if b.Config != nil {
		in.Config = *b.Config
	}
	return in
}

func (h *aiHandlers) GetAISettings(ctx context.Context, _ gen.GetAISettingsRequestObject) (gen.GetAISettingsResponseObject, error) {
	s, err := h.svc.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetAISettings200JSONResponse(toAISettings(s)), nil
}

func (h *aiHandlers) UpdateAISettings(ctx context.Context, req gen.UpdateAISettingsRequestObject) (gen.UpdateAISettingsResponseObject, error) {
	s, err := h.svc.UpdateSettings(ctx, PrincipalFrom(ctx), aiInput(*req.Body), requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.UpdateAISettings200JSONResponse(toAISettings(s)), nil
}

func (h *aiHandlers) TestAISettings(ctx context.Context, req gen.TestAISettingsRequestObject) (gen.TestAISettingsResponseObject, error) {
	r, err := h.svc.TestSettings(ctx, aiInput(*req.Body))
	if err != nil {
		return nil, err
	}
	out := gen.AITestResult{Ok: r.OK}
	if r.ErrorCode != "" {
		out.ErrorCode, out.ErrorMessage = &r.ErrorCode, &r.ErrorMessage
	}
	return gen.TestAISettings200JSONResponse(out), nil
}

func (h *aiHandlers) GetAIStatus(ctx context.Context, _ gen.GetAIStatusRequestObject) (gen.GetAIStatusResponseObject, error) {
	s, err := h.svc.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetAIStatus200JSONResponse{Enabled: s.Enabled(), Provider: s.Provider}, nil
}

func (h *aiHandlers) GenerateAIProposal(ctx context.Context, req gen.GenerateAIProposalRequestObject) (gen.GenerateAIProposalResponseObject, error) {
	p := PrincipalFrom(ctx)
	if ok, wait := h.limiter.allow(p.UserID.String()); !ok {
		return nil, &apperr.Error{Kind: apperr.KindTooManyRequests, Code: CodeAITooManyRequests, RetryAfter: wait}
	}
	b := req.Body
	in := ai.Input{Task: string(b.Task), Prompt: b.Prompt, Locale: "en"}
	if b.ConnectionId != nil {
		in.ConnectionID = *b.ConnectionId
	}
	if b.Sql != nil {
		in.SQL = *b.Sql
	}
	if b.Timezone != nil {
		in.Timezone = *b.Timezone
	}
	if u, err := h.auth.Me(ctx, p); err == nil && u.Locale != "" {
		in.Locale = u.Locale
	}
	prop, err := h.svc.Generate(ctx, in)
	if err != nil {
		return nil, err
	}
	out := gen.AIProposal{
		Task: gen.AIProposalTask(prop.Task), Sql: prop.SQL, Explanation: prop.Explanation, SuggestedName: prop.SuggestedName,
		Params: make([]gen.QueryParam, len(prop.Params)), Warnings: prop.Warnings, Model: prop.Model,
	}
	for i, d := range prop.Params {
		out.Params[i] = gen.QueryParam{Name: d.Name, Type: gen.ParamType(d.Type), Default: d.Default}
	}
	if s := prop.Schedule; s != nil {
		out.Schedule = &gen.AISchedule{Cron: s.Cron, Timezone: s.Timezone, Description: s.Description, Next: s.Next}
	}
	return gen.GenerateAIProposal200JSONResponse(out), nil
}
