// Package anthropic is the AI provider for Claude models, through the official Go SDK on the
// Messages API with structured outputs (output_config.format), so answers follow the schema.
package anthropic

import (
	"context"
	"embed"
	"errors"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/rowbird/rowbird/internal/ai/provider"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the provider id.
const ID = "anthropic"

// Defaults.
const (
	DefaultBaseURL = "https://api.anthropic.com"
	DefaultModel   = "claude-opus-5"
	maxTokens      = 16000
)

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MergeMessages(provider.Messages(), plugin.MustLoadMessages(locales))

func init() {
	plugin.Register(plugin.KindAIProvider, ID, func() plugin.Plugin { return claude{} })
}

type claude struct{}

func (claude) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.anthropic.name", Description: "plugin.anthropic.description", Icon: "sparkles", Version: "1.0.0"}
}

func (claude) ConfigSchema() *plugin.Schema {
	model := provider.Model("plugin.anthropic.model.help")
	model.Default = DefaultModel
	base := provider.BaseURL(DefaultBaseURL, false, "plugin.anthropic.base_url.help")
	base.Group = "advanced"
	return &plugin.Schema{Fields: []plugin.Field{provider.APIKey(true), model, base, provider.Timeout()}}
}

func (claude) Capabilities() any         { return plugin.AICapabilities{Structured: "json_schema"} }
func (claude) Messages() plugin.Messages { return messages }

// client uses only the configuration: environment variables and profiles of the host are ignored,
// requests go through the policy-bound HTTP client, and failures are not retried (the user asks
// again).
func client(env plugin.AIEnv) sdk.Client {
	base := provider.String(env.Config, "base_url")
	if base == "" {
		base = DefaultBaseURL
	}
	return sdk.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(base),
		option.WithAPIKey(provider.String(env.Config, "api_key")),
		option.WithHTTPClient(env.HTTP),
		option.WithMaxRetries(0),
	)
}

func (claude) Complete(ctx context.Context, env plugin.AIEnv, req plugin.AIRequest) (plugin.AIResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, provider.TimeoutOf(env.Config))
	defer cancel()
	c := client(env)
	msg, err := c.Messages.New(ctx, sdk.MessageNewParams{
		Model:        provider.String(env.Config, "model"),
		MaxTokens:    maxTokens,
		System:       []sdk.TextBlockParam{{Text: req.System}},
		Messages:     []sdk.MessageParam{sdk.NewUserMessage(sdk.NewTextBlock(req.User))},
		OutputConfig: sdk.OutputConfigParam{Format: sdk.JSONOutputFormatParam{Schema: req.Output}},
	})
	if err != nil {
		return plugin.AIResponse{}, mapError(err)
	}
	switch msg.StopReason {
	case sdk.StopReasonRefusal:
		return plugin.AIResponse{}, provider.Err(plugin.ErrCodeAIRejected, errors.New("the model declined the request"))
	case sdk.StopReasonMaxTokens:
		return plugin.AIResponse{}, provider.Err(plugin.ErrCodeAIInvalidOutput, errors.New("the answer was cut at the token limit"))
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(sdk.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	out, err := provider.Output(text.String())
	if err != nil {
		return plugin.AIResponse{}, err
	}
	return plugin.AIResponse{
		JSON: out, Model: msg.Model,
		Usage: plugin.AIUsage{InputTokens: msg.Usage.InputTokens, OutputTokens: msg.Usage.OutputTokens},
	}, nil
}

// Test reads the configured model, which checks the key and the model without spending tokens.
func (claude) Test(ctx context.Context, env plugin.AIEnv) error {
	ctx, cancel := context.WithTimeout(ctx, provider.TimeoutOf(env.Config))
	defer cancel()
	c := client(env)
	if _, err := c.Models.Get(ctx, provider.String(env.Config, "model"), sdk.ModelGetParams{}); err != nil {
		return mapError(err)
	}
	return nil
}

func mapError(err error) error {
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		return provider.StatusError(apiErr.StatusCode, apiErr.Error())
	}
	return provider.NetworkError(err)
}
