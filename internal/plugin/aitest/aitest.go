// Package aitest is the conformance suite every AI provider must pass: metadata and translations,
// a secret API key unless the provider is local, the prompt and the answer schema reaching the
// API, the JSON answer returned, failures mapped to stable codes with secrets kept out of errors,
// and cancellation. It also has Fake, a provider for tests of the AI service.
package aitest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/secretconfig"
)

// Answer is the JSON the fake services answer with, inside each API's envelope.
const Answer = `{"sql":"select 1","explanation":"One row."}`

// Request is the completion the suite sends.
func Request() plugin.AIRequest {
	return plugin.AIRequest{
		System:     "You write SQL for the sqlite dialect.",
		User:       "Count the orders placed today.",
		OutputName: "proposal",
		Output: map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"sql", "explanation"},
			"properties": map[string]any{"sql": map[string]any{"type": "string"}, "explanation": map[string]any{"type": "string"}},
		},
	}
}

// Harness describes how to exercise one provider against a fake API.
type Harness struct {
	Provider plugin.AIProvider
	// Config is a valid configuration pointing at the fake API.
	Config map[string]any
	// LastBody returns the body of the last request the fake received.
	LastBody func() string
	// Fail makes the fake answer with status, echoing the configuration's secrets in its body; 0
	// resets.
	Fail func(status int)
}

// Env builds the environment with the validated configuration.
func (h Harness) Env(t *testing.T) plugin.AIEnv {
	t.Helper()
	cfg, err := h.Provider.ConfigSchema().Validate(h.Config)
	if err != nil {
		t.Fatalf("config does not validate: %v", err)
	}
	dial := netx.NewDialer(netx.PolicyOpen).DialContext
	return plugin.AIEnv{Config: cfg, HTTP: netx.HTTPClient(dial, 30*time.Second)}
}

// Run executes the suite.
func Run(t *testing.T, h Harness) {
	p := h.Provider
	caps, _ := p.Capabilities().(plugin.AICapabilities)

	t.Run("metadata", func(t *testing.T) {
		m := p.Meta()
		if m.ID == "" || m.Version == "" {
			t.Fatal("metadata missing")
		}
		switch caps.Structured {
		case "json_schema", "tool", "json":
		default:
			t.Errorf("unknown structured output %q", caps.Structured)
		}
		key, ok := p.ConfigSchema().Field("api_key")
		if !caps.Local && (!ok || !key.Secret) {
			t.Error("remote providers need a secret api_key")
		}
		if _, ok := p.ConfigSchema().Field("model"); !ok {
			t.Error("no model field")
		}
		keys := []string{m.Name, m.Description}
		for _, f := range p.ConfigSchema().Fields {
			keys = append(keys, f.Label)
			if f.Help != "" {
				keys = append(keys, f.Help)
			}
			for _, v := range f.Enum {
				keys = append(keys, strings.TrimSuffix(f.Label, ".label")+"."+v)
			}
		}
		for _, locale := range []string{"en", "pt-BR"} {
			for _, k := range keys {
				if p.Messages()[locale][k] == "" {
					t.Errorf("%s has no %s translation for %s", m.ID, locale, k)
				}
			}
		}
	})

	t.Run("complete", func(t *testing.T) {
		h.Fail(0)
		res, err := p.Complete(context.Background(), h.Env(t), Request())
		if err != nil {
			t.Fatal(err)
		}
		var got, want map[string]any
		if err := json.Unmarshal(res.JSON, &got); err != nil {
			t.Fatalf("answer %s: %v", res.JSON, err)
		}
		_ = json.Unmarshal([]byte(Answer), &want)
		if got["sql"] != want["sql"] || got["explanation"] != want["explanation"] {
			t.Errorf("answer %s", res.JSON)
		}
		body := h.LastBody()
		req := Request()
		for _, s := range []string{req.System, req.User, "explanation"} {
			if !strings.Contains(body, s) {
				t.Errorf("the request does not carry %s: %s", s, body)
			}
		}
	})

	t.Run("test", func(t *testing.T) {
		h.Fail(0)
		if err := p.Test(context.Background(), h.Env(t)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("failures", func(t *testing.T) {
		env := h.Env(t)
		cases := map[int]string{401: plugin.ErrCodeAIAuth, 429: plugin.ErrCodeAIRateLimited, 500: plugin.ErrCodeAIFailed, 400: plugin.ErrCodeAIRejected}
		for status, code := range cases {
			h.Fail(status)
			for _, call := range []func() error{
				func() error { _, err := p.Complete(context.Background(), env, Request()); return err },
				func() error { return p.Test(context.Background(), env) },
			} {
				err := call()
				var ae *plugin.AIError
				if !errors.As(err, &ae) || ae.Code != code {
					t.Errorf("HTTP %d: got %v, want %s", status, err, code)
					continue
				}
				_, secrets := p.ConfigSchema().Split(env.Config)
				if msg := secretconfig.Scrub(err.Error(), p.ConfigSchema(), env.Config); secretLeft(msg, secrets) {
					t.Errorf("HTTP %d: a secret survives scrubbing: %s", status, msg)
				}
			}
		}
		h.Fail(0)
	})

	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := p.Complete(ctx, h.Env(t), Request()); err == nil {
			t.Error("a cancelled completion succeeded")
		}
	})
}

func secretLeft(msg string, secrets map[string]any) bool {
	for _, v := range secrets {
		if s, ok := v.(string); ok && s != "" && strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
