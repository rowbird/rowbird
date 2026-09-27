// Package ai is the AI assistant (docs/spec/03-flows.md section 3, 04 "AI providers"): it builds
// the prompt from the user's request, the connection's dialect and schema (without the tables
// excluded from AI, never rows), asks the configured provider for structured JSON, and validates
// the answer into a proposal. Nothing it returns is executed or saved: the user applies it.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/connections"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/secretconfig"
	"github.com/rowbird/rowbird/internal/sqlscan"
	"github.com/rowbird/rowbird/internal/store"
)

// Tasks.
const (
	TaskQuery    = "query"
	TaskSchedule = "schedule"
)

// Limits of a request.
const (
	MaxPromptChars = 2000
	MaxSQLChars    = 20000
)

// Errors.
var (
	ErrNotConfigured = apperr.New(apperr.KindConflict, "ai.not_configured")
	ErrNoSchema      = apperr.New(apperr.KindConflict, "ai.no_schema")
)

// Service answers AI requests with proposals.
type Service struct {
	store    *store.Store
	conns    *connections.Service
	secrets  *secretconfig.Codec
	dial     netx.DialFunc
	logger   *slog.Logger
	now      func() time.Time
	provider func(id string) (plugin.AIProvider, bool)
}

// Options configure the service.
type Options struct {
	Dial   netx.DialFunc
	Logger *slog.Logger
	Now    func() time.Time
	// Provider looks providers up; tests replace the registry with a fake.
	Provider func(id string) (plugin.AIProvider, bool)
}

// NewService builds the service.
func NewService(st *store.Store, kr *crypto.Keyring, conns *connections.Service, opts Options) *Service {
	s := &Service{store: st, conns: conns, secrets: secretconfig.New(kr, SecretsKind), dial: opts.Dial, logger: opts.Logger, now: opts.Now, provider: opts.Provider}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.dial == nil {
		s.dial = netx.NewDialer(netx.PolicyOpen).DialContext
	}
	if s.provider == nil {
		s.provider = Provider
	}
	return s
}

// Provider returns the registered AI provider with id.
func Provider(id string) (plugin.AIProvider, bool) {
	p, ok := plugin.Get(plugin.KindAIProvider, id)
	if !ok {
		return nil, false
	}
	ai, ok := p.(plugin.AIProvider)
	return ai, ok
}

// Input is a request for a proposal.
type Input struct {
	Task   string
	Prompt string
	// ConnectionID and SQL are the query editor's connection and current SQL (task query).
	ConnectionID uuid.UUID
	SQL          string
	// Timezone is the report's (task schedule) or the browser's time zone.
	Timezone string
	// Locale is the language of the explanation and the suggested name.
	Locale string
}

// Generate asks the configured provider for a proposal.
func (s *Service) Generate(ctx context.Context, in Input) (*Proposal, error) {
	in.Prompt = strings.TrimSpace(in.Prompt)
	var fe []apperr.FieldError
	switch {
	case in.Prompt == "":
		fe = append(fe, apperr.Field("prompt", "validation.required"))
	case len([]rune(in.Prompt)) > MaxPromptChars:
		fe = append(fe, apperr.Field("prompt", "validation.too_long"))
	}
	if len(in.SQL) > MaxSQLChars {
		fe = append(fe, apperr.Field("sql", "validation.too_long"))
	}
	switch in.Task {
	case TaskQuery:
		if in.ConnectionID == uuid.Nil {
			fe = append(fe, apperr.Field("connection_id", "validation.required"))
		}
	case TaskSchedule:
	default:
		fe = append(fe, apperr.Field("task", "validation.invalid_value"))
	}
	if len(fe) > 0 {
		return nil, apperr.Invalid(fe...)
	}
	p, env, err := s.configured(ctx)
	if err != nil {
		return nil, err
	}
	var req plugin.AIRequest
	var pr prompt
	if in.Task == TaskQuery {
		if pr, err = s.queryPrompt(ctx, in); err != nil {
			return nil, err
		}
	} else {
		pr = schedulePrompt(in, s.now())
	}
	req = pr.request
	start := s.now()
	res, err := p.Complete(ctx, env, req)
	logger := s.logger.With("provider", p.Meta().ID, "task", in.Task, "duration_ms", s.now().Sub(start).Milliseconds())
	if err != nil {
		code := plugin.ErrCodeAIFailed
		var ae *plugin.AIError
		if errors.As(err, &ae) {
			code = ae.Code
		}
		logger.WarnContext(ctx, "ai request failed", "error_code", code, "error", secretconfig.Scrub(err.Error(), p.ConfigSchema(), env.Config))
		return nil, apperr.New(apperr.KindUnprocessable, code)
	}
	logger.InfoContext(ctx, "ai proposal", "model", res.Model, "input_tokens", res.Usage.InputTokens, "output_tokens", res.Usage.OutputTokens)
	prop, err := validate(in, pr, res.JSON, s.now())
	if err != nil {
		logger.WarnContext(ctx, "ai answer rejected", "error", err)
		return nil, apperr.New(apperr.KindUnprocessable, plugin.ErrCodeAIInvalidOutput)
	}
	prop.Model = res.Model
	return prop, nil
}

// queryPrompt reads the connection's dialect and cached schema.
func (s *Service) queryPrompt(ctx context.Context, in Input) (prompt, error) {
	c, err := s.store.Connections().Get(ctx, in.ConnectionID)
	if err != nil {
		return prompt{}, err
	}
	dialect := sqlscan.Postgres
	if p, ok := plugin.Get(plugin.KindConnector, c.Driver); ok {
		if caps, ok := p.Capabilities().(plugin.ConnectorCapabilities); ok && caps.Dialect != "" {
			dialect = sqlscan.Dialect(caps.Dialect)
		}
	}
	schema, cachedAt, err := s.conns.Schema(ctx, c.ID)
	if err != nil {
		return prompt{}, err
	}
	if cachedAt == nil {
		return prompt{}, ErrNoSchema
	}
	return queryPrompt(in, dialect, schema, c.AIExcludedTables), nil
}

// decode reads the model's JSON answer.
func decode(raw []byte, v any) error { return json.Unmarshal(raw, v) }
