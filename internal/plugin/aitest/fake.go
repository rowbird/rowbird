package aitest

import (
	"context"
	"sync"

	"github.com/rowbird/rowbird/internal/plugin"
)

// FakeID is the id of the fake provider.
const FakeID = "fake"

// Fake is an AI provider for tests: it records requests and answers with what the test sets.
type Fake struct {
	mu       sync.Mutex
	answer   string
	err      error
	requests []plugin.AIRequest
}

// NewFake answers with Answer until told otherwise.
func NewFake() *Fake { return &Fake{answer: Answer} }

// Answer sets the next answers; err, when not nil, makes completions fail instead.
func (f *Fake) Answer(json string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer, f.err = json, err
}

// Requests returns the completions received so far.
func (f *Fake) Requests() []plugin.AIRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]plugin.AIRequest(nil), f.requests...)
}

func (f *Fake) Meta() plugin.Metadata {
	return plugin.Metadata{ID: FakeID, Name: "fake.name", Description: "fake.description", Version: "1"}
}

func (f *Fake) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "api_key", Type: plugin.TypeString, Secret: true, Label: "l"},
		{Key: "model", Type: plugin.TypeString, Required: true, Label: "l"},
	}}
}

func (f *Fake) Capabilities() any         { return plugin.AICapabilities{Structured: "json_schema"} }
func (f *Fake) Messages() plugin.Messages { return nil }

func (f *Fake) Complete(ctx context.Context, env plugin.AIEnv, req plugin.AIRequest) (plugin.AIResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if err := ctx.Err(); err != nil {
		return plugin.AIResponse{}, err
	}
	if f.err != nil {
		return plugin.AIResponse{}, f.err
	}
	model, _ := env.Config["model"].(string)
	return plugin.AIResponse{JSON: []byte(f.answer), Model: model, Usage: plugin.AIUsage{InputTokens: 10, OutputTokens: 5}}, nil
}

func (f *Fake) Test(context.Context, plugin.AIEnv) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}
