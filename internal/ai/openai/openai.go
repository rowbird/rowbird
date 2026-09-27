// Package openai registers two AI providers on the Chat Completions API: "openai", which asks for
// JSON matching the answer schema (structured outputs), and "openai_compatible", for services that
// copy the API (OpenRouter, LM Studio, vLLM, Groq, ...), which use JSON mode by default because not
// all of them enforce a schema.
package openai

import (
	"context"
	"embed"
	"errors"
	"net/url"
	"strings"

	"github.com/rowbird/rowbird/internal/ai/provider"
	"github.com/rowbird/rowbird/internal/plugin"
)

// Provider ids.
const (
	ID           = "openai"
	CompatibleID = "openai_compatible"
)

// DefaultBaseURL is OpenAI's API.
const DefaultBaseURL = "https://api.openai.com/v1"

// Structured output modes of openai_compatible.
const (
	ModeJSONSchema = "json_schema"
	ModeJSONObject = "json_object"
)

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MergeMessages(provider.Messages(), plugin.MustLoadMessages(locales))

func init() {
	plugin.Register(plugin.KindAIProvider, ID, func() plugin.Plugin { return client{id: ID} })
	plugin.Register(plugin.KindAIProvider, CompatibleID, func() plugin.Plugin { return client{id: CompatibleID} })
}

type client struct{ id string }

func (c client) compatible() bool { return c.id == CompatibleID }

func (c client) Meta() plugin.Metadata {
	return plugin.Metadata{ID: c.id, Name: "plugin." + c.id + ".name", Description: "plugin." + c.id + ".description", Icon: "sparkles", Version: "1.0.0"}
}

func (c client) ConfigSchema() *plugin.Schema {
	if c.compatible() {
		return &plugin.Schema{Fields: []plugin.Field{
			provider.BaseURL("", true, "plugin.openai_compatible.base_url.help"),
			provider.APIKey(false),
			provider.Model("plugin.openai_compatible.model.help"),
			{Key: "structured", Type: plugin.TypeString, Default: ModeJSONObject, Enum: []string{ModeJSONObject, ModeJSONSchema}, Group: "advanced", Label: "plugin.openai_compatible.structured.label", Help: "plugin.openai_compatible.structured.help"},
			provider.Timeout(),
		}}
	}
	base := provider.BaseURL(DefaultBaseURL, false, "plugin.openai.base_url.help")
	base.Group = "advanced"
	return &plugin.Schema{Fields: []plugin.Field{
		provider.APIKey(true),
		provider.Model("plugin.openai.model.help"),
		base,
		provider.Timeout(),
	}}
}

func (c client) Capabilities() any {
	if c.compatible() {
		return plugin.AICapabilities{Structured: "json"}
	}
	return plugin.AICapabilities{Structured: "json_schema"}
}

func (c client) Messages() plugin.Messages { return messages }

func (c client) endpoint(env plugin.AIEnv, path string) string {
	base := provider.String(env.Config, "base_url")
	if base == "" {
		base = DefaultBaseURL
	}
	return strings.TrimRight(base, "/") + path
}

func headers(env plugin.AIEnv) map[string]string {
	h := map[string]string{}
	if key := provider.String(env.Config, "api_key"); key != "" {
		h["Authorization"] = "Bearer " + key
	}
	return h
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content *string `json:"content"`
			Refusal *string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func (c client) Complete(ctx context.Context, env plugin.AIEnv, req plugin.AIRequest) (plugin.AIResponse, error) {
	mode := ModeJSONSchema
	if c.compatible() {
		if m := provider.String(env.Config, "structured"); m != "" {
			mode = m
		}
	}
	system := req.System
	if mode == ModeJSONObject {
		system = provider.WithSchema(req)
	}
	body := map[string]any{
		"model": provider.String(env.Config, "model"),
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": req.User},
		},
	}
	if mode == ModeJSONSchema {
		body["response_format"] = map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": req.OutputName, "schema": req.Output, "strict": true},
		}
	} else {
		body["response_format"] = map[string]any{"type": "json_object"}
	}
	var res chatResponse
	if err := provider.PostJSON(ctx, env, c.endpoint(env, "/chat/completions"), headers(env), body, &res); err != nil {
		return plugin.AIResponse{}, err
	}
	if len(res.Choices) == 0 {
		return plugin.AIResponse{}, provider.Err(plugin.ErrCodeAIInvalidOutput, errors.New("no choices in the answer"))
	}
	msg := res.Choices[0].Message
	if msg.Refusal != nil && *msg.Refusal != "" {
		return plugin.AIResponse{}, provider.Err(plugin.ErrCodeAIRejected, errors.New("the model refused: "+*msg.Refusal))
	}
	if msg.Content == nil {
		return plugin.AIResponse{}, provider.Err(plugin.ErrCodeAIInvalidOutput, errors.New("empty answer ("+res.Choices[0].FinishReason+")"))
	}
	out, err := provider.Output(*msg.Content)
	if err != nil {
		return plugin.AIResponse{}, err
	}
	return plugin.AIResponse{
		JSON: out, Model: res.Model,
		Usage: plugin.AIUsage{InputTokens: res.Usage.PromptTokens, OutputTokens: res.Usage.CompletionTokens},
	}, nil
}

// Test reads the configured model, which checks the key and that the model exists without
// spending tokens.
func (c client) Test(ctx context.Context, env plugin.AIEnv) error {
	return provider.GetJSON(ctx, env, c.endpoint(env, "/models/"+url.PathEscape(provider.String(env.Config, "model"))), headers(env), nil)
}
