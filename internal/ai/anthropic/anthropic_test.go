package anthropic

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
	mu     sync.Mutex
	last   string
	fail   int
	answer string
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.last = string(b)
	fail, answer := f.fail, f.answer
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if fail != 0 {
		w.WriteHeader(fail)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"key ` + r.Header.Get("X-Api-Key") + `"}}`))
		return
	}
	if r.Header.Get("X-Api-Key") != "sk-ant-test-secret" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
		return
	}
	switch r.URL.Path {
	case "/v1/messages":
		if answer == "" {
			text, _ := json.Marshal(aitest.Answer)
			answer = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":` + string(text) + `}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":7}}`
		}
		_, _ = w.Write([]byte(answer))
	case "/v1/models/claude-test":
		_, _ = w.Write([]byte(`{"id":"claude-test","type":"model","display_name":"Test","created_at":"2026-01-01T00:00:00Z"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"not found"}}`))
	}
}

func harness(t *testing.T) (aitest.Harness, *fake) {
	f := &fake{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return aitest.Harness{
		Provider: claude{},
		Config:   map[string]any{"base_url": srv.URL, "api_key": "sk-ant-test-secret", "model": "claude-test"},
		LastBody: func() string { f.mu.Lock(); defer f.mu.Unlock(); return f.last },
		Fail:     func(s int) { f.mu.Lock(); f.fail = s; f.mu.Unlock() },
	}, f
}

func TestConformance(t *testing.T) {
	h, _ := harness(t)
	aitest.Run(t, h)
}

func TestStructuredOutputAndHostCredentials(t *testing.T) {
	// Credentials of the host must never replace the configured key.
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-host")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:1")
	h, f := harness(t)
	res, err := h.Provider.Complete(t.Context(), h.Env(t), aitest.Request())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.last, `"output_config":{"format":{"schema":`) || res.Usage.InputTokens != 12 {
		t.Errorf("request %s usage %+v", f.last, res.Usage)
	}
}

func TestRefusalAndTruncation(t *testing.T) {
	for stop, code := range map[string]string{"refusal": plugin.ErrCodeAIRejected, "max_tokens": plugin.ErrCodeAIInvalidOutput} {
		h, f := harness(t)
		f.answer = `{"id":"m","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":"` + stop + `","usage":{"input_tokens":1,"output_tokens":1}}`
		_, err := h.Provider.Complete(t.Context(), h.Env(t), aitest.Request())
		var ae *plugin.AIError
		if !errors.As(err, &ae) || ae.Code != code {
			t.Errorf("%s: %v, want %s", stop, err, code)
		}
	}
}
