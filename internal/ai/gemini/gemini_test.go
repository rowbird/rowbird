package gemini

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
	query  string
	fail   int
	answer string
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.last, f.query = string(b), r.URL.RawQuery
	fail, answer := f.fail, f.answer
	f.mu.Unlock()
	if fail != 0 {
		w.WriteHeader(fail)
		_, _ = w.Write([]byte(`{"error":{"message":"key ` + r.Header.Get("X-Goog-Api-Key") + `"}}`))
		return
	}
	if r.Header.Get("X-Goog-Api-Key") != "gm-test-secret" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/v1beta/models/gemini-test:generateContent":
		if answer == "" {
			text, _ := json.Marshal(aitest.Answer)
			answer = `{"candidates":[{"content":{"parts":[{"text":` + string(text) + `}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":7},"modelVersion":"gemini-test"}`
		}
		_, _ = w.Write([]byte(answer))
	case "/v1beta/models/gemini-test":
		_, _ = w.Write([]byte(`{"name":"models/gemini-test"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func harness(t *testing.T) (aitest.Harness, *fake) {
	f := &fake{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return aitest.Harness{
		Provider: gemini{},
		Config:   map[string]any{"base_url": srv.URL + "/v1beta", "api_key": "gm-test-secret", "model": "gemini-test"},
		LastBody: func() string { f.mu.Lock(); defer f.mu.Unlock(); return f.last },
		Fail:     func(s int) { f.mu.Lock(); f.fail = s; f.mu.Unlock() },
	}, f
}

func TestConformance(t *testing.T) {
	h, f := harness(t)
	aitest.Run(t, h)
	if strings.Contains(f.query, "secret") {
		t.Error("the key went in the URL")
	}
}

func TestBlockedAndCutAnswers(t *testing.T) {
	for body, code := range map[string]string{
		`{"promptFeedback":{"blockReason":"SAFETY"}}`:                                   plugin.ErrCodeAIRejected,
		`{"candidates":[{"content":{"parts":[]},"finishReason":"MAX_TOKENS"}]}`:         plugin.ErrCodeAIInvalidOutput,
		`{"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":"SAFETY"}]}`: plugin.ErrCodeAIRejected,
	} {
		h, f := harness(t)
		f.answer = body
		_, err := h.Provider.Complete(t.Context(), h.Env(t), aitest.Request())
		var ae *plugin.AIError
		if !errors.As(err, &ae) || ae.Code != code {
			t.Errorf("%s: %v, want %s", body, err, code)
		}
	}
}
