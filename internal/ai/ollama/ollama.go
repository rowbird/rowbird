// Package ollama is the AI provider for models served by Ollama on the user's network, on its chat
// API with the answer schema as the output format. It needs no key.
package ollama

import (
	"context"
	"embed"
	"strings"

	"github.com/rowbird/rowbird/internal/ai/provider"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the provider id.
const ID = "ollama"

// DefaultBaseURL is Ollama's default address.
const DefaultBaseURL = "http://localhost:11434"

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MergeMessages(provider.Messages(), plugin.MustLoadMessages(locales))

func init() {
	plugin.Register(plugin.KindAIProvider, ID, func() plugin.Plugin { return ollama{} })
}

type ollama struct{}

func (ollama) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.ollama.name", Description: "plugin.ollama.description", Icon: "sparkles", Version: "1.0.0"}
}

func (ollama) ConfigSchema() *plugin.Schema {
	timeout := provider.Timeout()
	timeout.Default = int64(180) // local models are slower
	key := provider.APIKey(false)
	key.Group, key.Help = "advanced", "plugin.ollama.api_key.help"
	return &plugin.Schema{Fields: []plugin.Field{
		provider.BaseURL(DefaultBaseURL, true, "plugin.ollama.base_url.help"),
		provider.Model("plugin.ollama.model.help"),
		key,
		timeout,
	}}
}

func (ollama) Capabilities() any {
	return plugin.AICapabilities{Structured: "json_schema", Local: true}
}
func (ollama) Messages() plugin.Messages { return messages }

func endpoint(env plugin.AIEnv, path string) (string, map[string]string) {
	h := map[string]string{}
	if key := provider.String(env.Config, "api_key"); key != "" {
		h["Authorization"] = "Bearer " + key
	}
	return strings.TrimRight(provider.String(env.Config, "base_url"), "/") + path, h
}

type answer struct {
	Model   string `json:"model"`
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	PromptEvalCount int64 `json:"prompt_eval_count"`
	EvalCount       int64 `json:"eval_count"`
}

func (ollama) Complete(ctx context.Context, env plugin.AIEnv, req plugin.AIRequest) (plugin.AIResponse, error) {
	u, headers := endpoint(env, "/api/chat")
	body := map[string]any{
		"model":  provider.String(env.Config, "model"),
		"stream": false,
		"format": req.Output,
		"messages": []map[string]string{
			// Small models follow the schema better when the instructions show it too.
			{"role": "system", "content": provider.WithSchema(req)},
			{"role": "user", "content": req.User},
		},
	}
	var res answer
	if err := provider.PostJSON(ctx, env, u, headers, body, &res); err != nil {
		return plugin.AIResponse{}, err
	}
	out, err := provider.Output(res.Message.Content)
	if err != nil {
		return plugin.AIResponse{}, err
	}
	return plugin.AIResponse{JSON: out, Model: res.Model, Usage: plugin.AIUsage{InputTokens: res.PromptEvalCount, OutputTokens: res.EvalCount}}, nil
}

// Test asks Ollama about the configured model, which fails when it is not pulled.
func (ollama) Test(ctx context.Context, env plugin.AIEnv) error {
	u, headers := endpoint(env, "/api/show")
	return provider.PostJSON(ctx, env, u, headers, map[string]string{"model": provider.String(env.Config, "model")}, nil)
}
