package slack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/destinationtest"
)

const token = "xoxb-slack-secret-token"

type fake struct {
	mu        sync.Mutex
	url       string
	posts     []map[string]any
	uploads   map[string]string
	completed []map[string]any
	hooks     []map[string]any
	fail      int
	apiError  string
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != 0 {
		w.WriteHeader(f.fail)
		_, _ = w.Write([]byte("error " + r.Header.Get("Authorization")))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") && r.Header.Get("Authorization") != "Bearer "+token {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
		return
	}
	if f.apiError != "" {
		_, _ = w.Write([]byte(`{"ok":false,"error":"` + f.apiError + `"}`))
		return
	}
	var body map[string]any
	switch r.URL.Path {
	case "/api/chat.postMessage":
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.posts = append(f.posts, body)
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1.2"}`))
	case "/api/files.getUploadURLExternal":
		_ = r.ParseForm()
		id := "F" + r.Form.Get("length")
		_, _ = w.Write([]byte(`{"ok":true,"upload_url":"` + f.url + `/upload/` + id + `","file_id":"` + id + `"}`))
	case "/api/files.completeUploadExternal":
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.completed = append(f.completed, body)
		_, _ = w.Write([]byte(`{"ok":true}`))
	case "/api/auth.test":
		_, _ = w.Write([]byte(`{"ok":true}`))
	case "/hook":
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.hooks = append(f.hooks, body)
		_, _ = w.Write([]byte("ok"))
	default:
		if id, ok := strings.CutPrefix(r.URL.Path, "/upload/"); ok {
			b, _ := io.ReadAll(r.Body)
			f.uploads[id] = string(b)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
}

func setup(t *testing.T) (*fake, string) {
	f := &fake{uploads: map[string]string{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f, srv.URL
}

func TestConformanceBot(t *testing.T) {
	f, url := setup(t)
	destinationtest.Run(t, destinationtest.Harness{
		Destination: dest{}, Config: map[string]any{"auth": "bot", "bot_token": token, "api_base": url + "/api"},
		Options: map[string]any{"channel": "#sales"}, Inline: "```\nregiao  total\n```",
		Sent: func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.posts) },
		Fail: func(s int) { f.mu.Lock(); f.fail = s; f.mu.Unlock() },
	})
}

func TestConformanceWebhook(t *testing.T) {
	f, url := setup(t)
	destinationtest.Run(t, destinationtest.Harness{
		Destination: dest{}, Config: map[string]any{"auth": "webhook", "webhook_url": url + "/hook"},
		Sent: func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.hooks) },
		Fail: func(s int) { f.mu.Lock(); f.fail = s; f.mu.Unlock() },
	})
	if c := (dest{}).CapabilitiesFor(map[string]any{"auth": "webhook"}); c.SupportsAttachments {
		t.Error("an incoming webhook cannot attach files")
	}
}

func TestMessageAndFiles(t *testing.T) {
	f, url := setup(t)
	h := destinationtest.Harness{Destination: dest{}, Config: map[string]any{"bot_token": token, "api_base": url + "/api"}, Options: map[string]any{"channel": "#sales"}}
	msg := destinationtest.Message("en")
	res, err := (dest{}).Send(context.Background(), h.Env(t), msg)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := f.posts[0]["text"].(string)
	for _, want := range []string{"*Vendas por região &lt;Q3&gt; &amp; \"total\"*", "1,234 rows", "<https://rb.example.com/r/rbl_abc|vendas.xlsx>", "<https://rb.example.com/runs/a001|View the run>"} {
		if !strings.Contains(text, want) {
			t.Errorf("message lacks %q:\n%s", want, text)
		}
	}
	if f.uploads["F22"] != "regiao;total\r\nSul;10\r\n" || len(f.completed) != 1 || f.completed[0]["channel_id"] != "C123" || res.Meta["ts"] != "1.2" {
		t.Errorf("uploads %v completed %v meta %v", f.uploads, f.completed, res.Meta)
	}

	for code, want := range map[string]string{"invalid_auth": plugin.ErrCodeDeliveryAuth, "channel_not_found": plugin.ErrCodeDeliveryRejected, "ratelimited": plugin.ErrCodeDeliveryRateLimited} {
		f.mu.Lock()
		f.apiError = code
		f.mu.Unlock()
		_, err := (dest{}).Send(context.Background(), h.Env(t), msg)
		var de *plugin.DeliveryError
		if !errors.As(err, &de) || de.Code != want {
			t.Errorf("%s: %v", code, err)
		}
	}
	h.Options = map[string]any{}
	f.apiError = ""
	if _, err := (dest{}).Send(context.Background(), h.Env(t), msg); err == nil {
		t.Error("sent without a channel")
	}
}
