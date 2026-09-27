package openai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/aitest"
)

type fake struct {
	mu   sync.Mutex
	last string
	fail int
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.last = string(b)
	fail := f.fail
	f.mu.Unlock()
	if fail != 0 {
		w.WriteHeader(fail)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key ` + r.Header.Get("Authorization") + `"}}`))
		return
	}
	if r.Header.Get("Authorization") != "Bearer sk-test-secret" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/v1/chat/completions":
		content, _ := json.Marshal(aitest.Answer)
		_, _ = w.Write([]byte(`{"model":"gpt-test","choices":[{"message":{"content":` + string(content) + `},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":7}}`))
	case "/v1/models/gpt-test":
		_, _ = w.Write([]byte(`{"id":"gpt-test"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func harness(t *testing.T, id string, cfg map[string]any) (aitest.Harness, *fake) {
	f := &fake{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	p, _ := plugin.Get(plugin.KindAIProvider, id)
	cfg["base_url"] = srv.URL + "/v1"
	cfg["api_key"], cfg["model"] = "sk-test-secret", "gpt-test"
	return aitest.Harness{
		Provider: p.(plugin.AIProvider), Config: cfg,
		LastBody: func() string { f.mu.Lock(); defer f.mu.Unlock(); return f.last },
		Fail:     func(s int) { f.mu.Lock(); f.fail = s; f.mu.Unlock() },
	}, f
}

func TestConformance(t *testing.T) {
	for _, id := range []string{ID, CompatibleID} {
		t.Run(id, func(t *testing.T) {
			h, _ := harness(t, id, map[string]any{})
			aitest.Run(t, h)
		})
	}
}

func TestStructuredOutputModes(t *testing.T) {
	cases := []struct {
		id   string
		cfg  map[string]any
		want string
	}{
		{ID, map[string]any{}, `"type":"json_schema"`},
		{CompatibleID, map[string]any{}, `"type":"json_object"`},
		{CompatibleID, map[string]any{"structured": ModeJSONSchema}, `"strict":true`},
	}
	for _, c := range cases {
		h, f := harness(t, c.id, c.cfg)
		if _, err := h.Provider.Complete(t.Context(), h.Env(t), aitest.Request()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(f.last, c.want) {
			t.Errorf("%s %v: request %s lacks %s", c.id, c.cfg, f.last, c.want)
		}
	}
}

func TestRefusalAndEmptyAnswers(t *testing.T) {
	for body, code := range map[string]string{
		`{"choices":[{"message":{"content":null,"refusal":"I can't help with that."}}]}`: plugin.ErrCodeAIRejected,
		`{"choices":[{"message":{"content":"not json"}}]}`:                               plugin.ErrCodeAIInvalidOutput,
		`{"choices":[]}`: plugin.ErrCodeAIInvalidOutput,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		h := aitest.Harness{Provider: client{id: ID}, Config: map[string]any{"base_url": srv.URL, "api_key": "k", "model": "m"}}
		_, err := h.Provider.Complete(t.Context(), h.Env(t), aitest.Request())
		var ae *plugin.AIError
		if !errors.As(err, &ae) || ae.Code != code {
			t.Errorf("%s: %v, want %s", body, err, code)
		}
		srv.Close()
	}
}
