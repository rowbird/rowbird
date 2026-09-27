// Package gemini is the AI provider for Google's Gemini models, on the generateContent REST API
// with a JSON response schema.
package gemini

import (
	"context"
	"embed"
	"errors"
	"net/url"
	"strings"

	"github.com/rowbird/rowbird/internal/ai/provider"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the provider id.
const ID = "gemini"

// DefaultBaseURL is Google's Generative Language API.
const DefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MergeMessages(provider.Messages(), plugin.MustLoadMessages(locales))

func init() {
	plugin.Register(plugin.KindAIProvider, ID, func() plugin.Plugin { return gemini{} })
}

type gemini struct{}

func (gemini) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.gemini.name", Description: "plugin.gemini.description", Icon: "sparkles", Version: "1.0.0"}
}

func (gemini) ConfigSchema() *plugin.Schema {
	base := provider.BaseURL(DefaultBaseURL, false, "plugin.gemini.base_url.help")
	base.Group = "advanced"
	return &plugin.Schema{Fields: []plugin.Field{provider.APIKey(true), provider.Model("plugin.gemini.model.help"), base, provider.Timeout()}}
}

func (gemini) Capabilities() any         { return plugin.AICapabilities{Structured: "json_schema"} }
func (gemini) Messages() plugin.Messages { return messages }

// model returns the URL of the configured model; the key goes in a header, never in the URL.
func model(env plugin.AIEnv) (string, map[string]string) {
	base := provider.String(env.Config, "base_url")
	if base == "" {
		base = DefaultBaseURL
	}
	name := strings.TrimPrefix(provider.String(env.Config, "model"), "models/")
	return strings.TrimRight(base, "/") + "/models/" + url.PathEscape(name), map[string]string{"x-goog-api-key": provider.String(env.Config, "api_key")}
}

type answer struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string `json:"modelVersion"`
}

func (gemini) Complete(ctx context.Context, env plugin.AIEnv, req plugin.AIRequest) (plugin.AIResponse, error) {
	u, headers := model(env)
	body := map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": req.System}}},
		"contents":          []map[string]any{{"role": "user", "parts": []map[string]string{{"text": req.User}}}},
		"generationConfig":  map[string]any{"responseMimeType": "application/json", "responseJsonSchema": req.Output},
	}
	var res answer
	if err := provider.PostJSON(ctx, env, u+":generateContent", headers, body, &res); err != nil {
		return plugin.AIResponse{}, err
	}
	if res.PromptFeedback.BlockReason != "" {
		return plugin.AIResponse{}, provider.Err(plugin.ErrCodeAIRejected, errors.New("the request was blocked: "+res.PromptFeedback.BlockReason))
	}
	if len(res.Candidates) == 0 {
		return plugin.AIResponse{}, provider.Err(plugin.ErrCodeAIInvalidOutput, errors.New("no candidates in the answer"))
	}
	c := res.Candidates[0]
	switch c.FinishReason {
	case "", "STOP":
	case "MAX_TOKENS":
		return plugin.AIResponse{}, provider.Err(plugin.ErrCodeAIInvalidOutput, errors.New("the answer was cut at the token limit"))
	default:
		return plugin.AIResponse{}, provider.Err(plugin.ErrCodeAIRejected, errors.New("the model stopped: "+c.FinishReason))
	}
	var text strings.Builder
	for _, p := range c.Content.Parts {
		text.WriteString(p.Text)
	}
	out, err := provider.Output(text.String())
	if err != nil {
		return plugin.AIResponse{}, err
	}
	return plugin.AIResponse{
		JSON: out, Model: res.ModelVersion,
		Usage: plugin.AIUsage{InputTokens: res.UsageMetadata.PromptTokenCount, OutputTokens: res.UsageMetadata.CandidatesTokenCount},
	}, nil
}

// Test reads the configured model, which checks the key and the model without spending tokens.
func (gemini) Test(ctx context.Context, env plugin.AIEnv) error {
	u, headers := model(env)
	return provider.GetJSON(ctx, env, u, headers, nil)
}
