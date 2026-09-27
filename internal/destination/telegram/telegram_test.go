package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin/destinationtest"
)

const token = "123456:bot-token-secret"

type fake struct {
	mu       sync.Mutex
	messages []map[string]any
	docs     []string
	fail     int
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.URL.Path, "/bot"+token+"/") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if f.fail != 0 {
		w.WriteHeader(f.fail)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":` + strconv.Itoa(f.fail) + `,"description":"Unauthorized"}`))
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/bot"+token+"/") {
	case "sendMessage":
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		f.messages = append(f.messages, m)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	case "sendDocument":
		file, h, err := r.FormFile("document")
		if err != nil || r.FormValue("chat_id") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		b, _ := io.ReadAll(file)
		f.docs = append(f.docs, h.Filename+":"+string(b))
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":43}}`))
	case "getMe":
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1}}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func harness(url string, f *fake) destinationtest.Harness {
	return destinationtest.Harness{
		Destination: dest{}, Config: map[string]any{"bot_token": token, "api_base": url},
		Options: map[string]any{"chat_id": "-100123"},
		Inline:  "<pre>regiao  total\nSul        10</pre>",
		Sent:    func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.messages) },
		Fail:    func(s int) { f.mu.Lock(); f.fail = s; f.mu.Unlock() },
	}
}

func TestConformance(t *testing.T) {
	f := &fake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	destinationtest.Run(t, harness(srv.URL, f))
}

func TestMessage(t *testing.T) {
	f := &fake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	h := harness(srv.URL, f)
	msg := destinationtest.Message("pt-BR")
	msg.Inline = h.Inline
	res, err := (dest{}).Send(context.Background(), h.Env(t), msg)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := f.messages[0]["text"].(string)
	for _, want := range []string{
		"<b>Vendas por região &lt;Q3&gt; &amp; &#34;total&#34;</b>", "1.234 linhas", "<pre>regiao", `<a href="https://rb.example.com/r/rbl_abc">vendas.xlsx</a>`,
		"Os links expiram em", `<a href="https://rb.example.com/runs/a001">Ver a execução</a>`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message lacks %q:\n%s", want, text)
		}
	}
	if f.messages[0]["parse_mode"] != "HTML" || f.messages[0]["chat_id"] != "-100123" || res.Meta["message_id"] != int64(42) {
		t.Errorf("message %v meta %v", f.messages[0], res.Meta)
	}
	if len(f.docs) != 1 || !strings.HasPrefix(f.docs[0], "vendas-por-regiao-2026-09-25.csv:regiao;total") {
		t.Errorf("documents %v", f.docs)
	}
	// A huge inline result is dropped rather than cut in the middle of the HTML.
	msg.Inline = "<pre>" + strings.Repeat("x", 5000) + "</pre>"
	_, _ = (dest{}).Send(context.Background(), h.Env(t), msg)
	if text, _ := f.messages[1]["text"].(string); strings.Contains(text, "<pre>") || len([]rune(text)) > MaxText {
		t.Errorf("long message: %d chars", len([]rune(text)))
	}
}
