package s3

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin/destinationtest"
)

// fakeS3 accepts path-style PUTs and HEAD bucket, which is all the destination uses.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string]string
	types   map[string]string
	fail    int
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != 0 {
		w.WriteHeader(f.fail)
		return
	}
	if !strings.Contains(r.Header.Get("Authorization"), "Credential=AKIA-test/") {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodHead:
		if r.URL.Path != "/reports-bucket" && r.URL.Path != "/reports-bucket/" {
			w.WriteHeader(http.StatusNotFound)
		}
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		f.objects[r.URL.Path] = string(b)
		f.types[r.URL.Path] = r.Header.Get("Content-Type") + "|" + r.Header.Get("Content-Disposition")
		w.Header().Set("ETag", `"abc"`)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func harness(url string, f *fakeS3) destinationtest.Harness {
	return destinationtest.Harness{
		Destination: dest{},
		Config: map[string]any{
			"endpoint": url, "region": "us-east-1", "bucket": "reports-bucket", "access_key": "AKIA-test", "secret_key": "s3-secret-key-value", "path_style": true,
		},
		Sent: func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.objects) },
		Fail: func(s int) { f.mu.Lock(); f.fail = s; f.mu.Unlock() },
	}
}

func TestConformance(t *testing.T) {
	f := &fakeS3{objects: map[string]string{}, types: map[string]string{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	destinationtest.Run(t, harness(srv.URL, f))
	key := "/reports-bucket/reports/vendas-por-regiao/2026-09-25/vendas-por-regiao.csv"
	// Over plain HTTP minio signs the body in aws-chunked encoding; the payload is inside.
	if !strings.Contains(f.objects[key], "regiao;total\r\nSul;10\r\n") || f.types[key] != `text/csv; charset=utf-8|attachment; filename=vendas-por-regiao-2026-09-25.csv` {
		t.Errorf("objects %v types %v", f.objects, f.types)
	}
}

func TestKeys(t *testing.T) {
	h := harness("http://127.0.0.1:1", nil)
	msg := destinationtest.Message("en")
	cases := map[string]string{
		"":                                     "reports/vendas-por-regiao/2026-09-25/vendas-por-regiao.csv",
		"/exports//{{report.slug}}.{{format}}": "exports/vendas-por-regiao.csv",
		"../../{{report.slug}}.{{format}}":     "vendas-por-regiao.csv",
		"{{run.date}}/{{run.id}}.{{format}}":   "2026-09-25/01900000-0000-7000-8000-00000000a001.csv",
	}
	for tpl, want := range cases {
		h.Options = map[string]any{"path": tpl}
		if tpl == "" {
			h.Options = map[string]any{}
		}
		got, err := Key(h.Env(t), msg, msg.Attachments[0])
		if err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", tpl, got, err, want)
		}
	}
	h.Options = map[string]any{"path": "{{nope}}"}
	if _, err := Key(h.Env(t), msg, msg.Attachments[0]); err == nil {
		t.Error("unknown variable accepted")
	}
}
