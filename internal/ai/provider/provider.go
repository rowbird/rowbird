// Package provider holds what AI providers share: posting JSON with failures mapped to stable
// codes, the common configuration fields and their translations.
package provider

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
)

//go:embed locales/*.json
var locales embed.FS

var catalog = plugin.MustLoadMessages(locales)

// Messages are the translations of the common fields.
func Messages() plugin.Messages { return catalog }

// DefaultTimeout bounds a completion unless the provider's configuration says otherwise.
const DefaultTimeout = 60 * time.Second

// APIKey is the secret API key field.
func APIKey(required bool) plugin.Field {
	return plugin.Field{Key: "api_key", Type: plugin.TypeString, Required: required, Secret: true, MaxLength: 500, Label: "ai.api_key.label"}
}

// Model is the model name, with a provider-specific help listing examples.
func Model(help string) plugin.Field {
	return plugin.Field{Key: "model", Type: plugin.TypeString, Required: true, MaxLength: 200, Label: "ai.model.label", Help: help}
}

// BaseURL is the API address; required when there is no sensible default.
func BaseURL(def string, required bool, help string) plugin.Field {
	f := plugin.Field{Key: "base_url", Type: plugin.TypeString, Required: required, Format: "url", MaxLength: 500, Label: "ai.base_url.label", Help: help}
	if def != "" {
		f.Default = def
	}
	return f
}

// Timeout is how long a completion may take, in seconds.
func Timeout() plugin.Field {
	return plugin.Field{
		Key: "timeout_seconds", Type: plugin.TypeInteger, Default: int64(DefaultTimeout / time.Second),
		Minimum: plugin.Float(5), Maximum: plugin.Float(600), Group: "advanced", Label: "ai.timeout_seconds.label",
	}
}

// TimeoutOf reads the timeout field.
func TimeoutOf(cfg map[string]any) time.Duration {
	if n, ok := cfg["timeout_seconds"].(int64); ok && n > 0 {
		return time.Duration(n) * time.Second
	}
	return DefaultTimeout
}

// String reads a string setting.
func String(cfg map[string]any, key string) string {
	s, _ := cfg[key].(string)
	return s
}

// Err builds an AIError.
func Err(code string, err error) *plugin.AIError { return &plugin.AIError{Code: code, Err: err} }

// NetworkError maps a transport failure.
func NetworkError(err error) *plugin.AIError {
	var ne net.Error
	switch {
	case errors.Is(err, netx.ErrBlocked):
		return Err(plugin.ErrCodeAIBlocked, err)
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return Err(plugin.ErrCodeAITimeout, err)
	case errors.Is(err, context.Canceled):
		return Err(plugin.ErrCodeAIFailed, err)
	}
	return Err(plugin.ErrCodeAIUnreachable, err)
}

// StatusError maps an unsuccessful HTTP response; body is a short excerpt for the log.
func StatusError(status int, body string) *plugin.AIError {
	err := fmt.Errorf("HTTP %d: %s", status, body)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return Err(plugin.ErrCodeAIAuth, err)
	case status == http.StatusTooManyRequests:
		return Err(plugin.ErrCodeAIRateLimited, err)
	case status >= 500:
		return Err(plugin.ErrCodeAIFailed, err)
	}
	return Err(plugin.ErrCodeAIRejected, err)
}

// PostJSON sends body as JSON to url with headers, within the configured timeout, and decodes the
// answer into out. At most 4 MB of the answer is read.
func PostJSON(ctx context.Context, env plugin.AIEnv, url string, headers map[string]string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, TimeoutOf(env.Config))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return Err(plugin.ErrCodeAIRejected, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return do(env.HTTP, req, out)
}

// GetJSON is PostJSON for GET requests (listing models to test a configuration).
func GetJSON(ctx context.Context, env plugin.AIEnv, url string, headers map[string]string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, TimeoutOf(env.Config))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Err(plugin.ErrCodeAIRejected, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return do(env.HTTP, req, out)
}

func do(client *http.Client, req *http.Request, out any) error {
	// The URL is the provider's configuration; the client dials through the network policy.
	res, err := client.Do(req) //nolint:gosec // see above
	if err != nil {
		return NetworkError(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return NetworkError(err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return StatusError(res.StatusCode, excerpt(b))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return Err(plugin.ErrCodeAIInvalidOutput, fmt.Errorf("decode answer: %w", err))
	}
	return nil
}

func excerpt(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// Output parses the text a model returned as JSON, accepting a fenced code block around it (JSON
// modes of some local models add one).
func Output(text string) ([]byte, error) {
	s := strings.TrimSpace(text)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	if !json.Valid([]byte(s)) {
		return nil, Err(plugin.ErrCodeAIInvalidOutput, errors.New("the model did not answer with JSON"))
	}
	return []byte(s), nil
}

// WithSchema appends the answer schema to the instructions, for APIs whose JSON mode does not take
// a schema: the model then knows the shape it must answer with.
func WithSchema(req plugin.AIRequest) string {
	b, _ := json.Marshal(req.Output)
	return req.System + "\n\nAnswer only with a JSON object that matches this JSON Schema:\n" + string(b)
}
