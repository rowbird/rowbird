package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin/destinationtest"
)

type fake struct {
	mu       sync.Mutex
	payloads []map[string]any
	files    []string
	fail     int
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != 0 {
		w.WriteHeader(f.fail)
		_, _ = w.Write([]byte(`{"message":"` + r.URL.Path + `"}`))
		return
	}
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(`{"id":"1","name":"hook"}`))
		return
	}
	var payload map[string]any
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		_ = json.Unmarshal([]byte(r.FormValue("payload_json")), &payload)
		file, h, err := r.FormFile("files[0]")
		if err == nil {
			b, _ := io.ReadAll(file)
			f.files = append(f.files, h.Filename+":"+string(b))
		}
	} else {
		_ = json.NewDecoder(r.Body).Decode(&payload)
	}
	if r.URL.Query().Get("wait") != "true" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.payloads = append(f.payloads, payload)
	_, _ = w.Write([]byte(`{"id":"99"}`))
}

func TestConformance(t *testing.T) {
	f := &fake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	destinationtest.Run(t, destinationtest.Harness{
		Destination: dest{}, Config: map[string]any{"webhook_url": srv.URL + "/api/webhooks/1/discord-secret-token"},
		Options: map[string]any{"username": "Rowbird"}, Inline: "| regiao | total |\n| --- | ---: |\n| Sul | 10 |",
		Sent: func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.payloads) },
		Fail: func(s int) { f.mu.Lock(); f.fail = s; f.mu.Unlock() },
	})
	if len(f.files) == 0 || !strings.HasPrefix(f.files[0], "vendas-por-regiao-2026-09-25.csv:") {
		t.Errorf("files %v", f.files)
	}
	content, _ := f.payloads[0]["content"].(string)
	if !strings.Contains(content, "**Vendas por região <Q3> & \"total\"**") || !strings.Contains(content, "[vendas.xlsx](<https://rb.example.com/r/rbl_abc>)") || len([]rune(content)) > MaxText {
		t.Errorf("content %q", content)
	}
	if f.payloads[0]["username"] != "Rowbird" {
		t.Errorf("payload %v", f.payloads[0])
	}
}

func TestLimitFromConfig(t *testing.T) {
	if c := (dest{}).CapabilitiesFor(map[string]any{"max_attachment_mb": int64(25)}); c.MaxAttachmentBytes != 25<<20 {
		t.Errorf("limit %d", c.MaxAttachmentBytes)
	}
	h := destinationtest.Harness{Destination: dest{}, Config: map[string]any{"webhook_url": "http://x"}}
	msg := destinationtest.Message("en")
	msg.Inline = strings.Repeat("row\n", 1000)
	p, _ := (dest{}).Preview(context.Background(), h.Env(t), msg)
	if len([]rune(p.Body)) > MaxText || strings.Contains(p.Body, "row\nrow") {
		t.Errorf("long preview kept the inline table (%d chars)", len([]rune(p.Body)))
	}
}
