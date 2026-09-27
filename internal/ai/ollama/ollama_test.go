package ollama

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
		_, _ = w.Write([]byte(`{"error":"token ` + r.Header.Get("Authorization") + `"}`))
		return
	}
	switch r.URL.Path {
	case "/api/chat":
		// Some models wrap JSON in a code fence even in JSON mode.
		content, _ := json.Marshal("```json\n" + aitest.Answer + "\n```")
		_, _ = w.Write([]byte(`{"model":"qwen-test","message":{"role":"assistant","content":` + string(content) + `},"done":true,"prompt_eval_count":12,"eval_count":7}`))
	case "/api/show":
		if !strings.Contains(string(b), `"qwen-test"`) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"details":{}}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestConformance(t *testing.T) {
	f := &fake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	aitest.Run(t, aitest.Harness{
		Provider: ollama{},
		Config:   map[string]any{"base_url": srv.URL, "model": "qwen-test", "api_key": "proxy-secret"},
		LastBody: func() string { f.mu.Lock(); defer f.mu.Unlock(); return f.last },
		Fail:     func(s int) { f.mu.Lock(); f.fail = s; f.mu.Unlock() },
	})
}
