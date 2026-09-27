package plugin

import (
	"context"
	"encoding/json"
	"net/http"
)

// AIProvider turns a prompt into structured JSON through a language model (docs/spec/04-plugins.md,
// "AI providers"). The AI service builds the prompt and the schema of the answer; providers only
// carry them to their API with its structured output feature and return the JSON. They never see
// row data: the prompt holds the user's request and a database schema.
type AIProvider interface {
	Plugin
	// Complete asks the model for a JSON value matching req.Output. Errors should be AIError with a
	// stable code.
	Complete(ctx context.Context, env AIEnv, req AIRequest) (AIResponse, error)
	// Test checks the configuration (credentials, model) with the smallest request the API allows.
	Test(ctx context.Context, env AIEnv) error
}

// AIEnv is the provider's configuration and the network it must use.
type AIEnv struct {
	// Config is validated by ConfigSchema, secrets included.
	Config map[string]any
	// HTTP is a client bound to the network policy.
	HTTP *http.Client
}

// AIRequest is one completion.
type AIRequest struct {
	// System holds the instructions; User the request with its context (schema, current SQL).
	System string
	User   string
	// OutputName names the answer for APIs that want one (a tool or schema name).
	OutputName string
	// Output is the JSON Schema of the answer (an object).
	Output map[string]any
}

// AIResponse is the model's answer.
type AIResponse struct {
	// JSON is the answer, not yet validated against the output schema.
	JSON  json.RawMessage
	Model string
	Usage AIUsage
}

// AIUsage counts tokens, for logs.
type AIUsage struct {
	InputTokens  int64
	OutputTokens int64
}

// AICapabilities describe a provider to the UI.
type AICapabilities struct {
	// Structured is how the provider asks for JSON: "json_schema" (the API enforces the schema),
	// "tool" (a forced tool call) or "json" (JSON mode; the schema is only in the instructions).
	Structured string `json:"structured"`
	// Local providers run on the user's network and need no API key.
	Local bool `json:"local"`
}

// AI error codes.
const (
	ErrCodeAIAuth          = "ai.auth_failed"
	ErrCodeAIRejected      = "ai.rejected"
	ErrCodeAIUnreachable   = "ai.unreachable"
	ErrCodeAIRateLimited   = "ai.rate_limited"
	ErrCodeAITimeout       = "ai.timeout"
	ErrCodeAIBlocked       = "ai.network_blocked"
	ErrCodeAIFailed        = "ai.failed"
	ErrCodeAIInvalidOutput = "ai.invalid_output"
)

// AIError is a failed completion with a stable code. Err keeps the underlying error for logs,
// scrubbed of secrets before it is shown.
type AIError struct {
	Code string
	Err  error
}

func (e *AIError) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Err.Error()
	}
	return e.Code
}

func (e *AIError) Unwrap() error { return e.Err }
