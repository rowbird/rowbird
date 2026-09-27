package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var site = fstest.MapFS{
	"index.html":           {Data: []byte("<html>app</html>")},
	"favicon.svg":          {Data: []byte("<svg/>")},
	"assets/index-abc.js":  {Data: []byte("console.log(1)")},
	"assets/index-abc.css": {Data: []byte("body{}")},
}

func get(t *testing.T, h http.Handler, method, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec.Result()
}

func body(t *testing.T, r *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestServesFilesAndFallsBackToIndex(t *testing.T) {
	h := NewHandler(site)
	cases := []struct {
		path, wantBody, wantCache, wantType string
	}{
		{"/", "<html>app</html>", "no-cache", "text/html"},
		{"/index.html", "<html>app</html>", "no-cache", "text/html"},
		{"/reports/0192/edit", "<html>app</html>", "no-cache", "text/html"},
		{"/../../etc/passwd", "<html>app</html>", "no-cache", "text/html"},
		{"/favicon.svg", "<svg/>", "no-cache", "image/svg+xml"},
		{"/assets/index-abc.js", "console.log(1)", "public, max-age=31536000, immutable", "text/javascript"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			r := get(t, h, http.MethodGet, tc.path)
			if r.StatusCode != http.StatusOK {
				t.Fatalf("status %d", r.StatusCode)
			}
			if got := body(t, r); got != tc.wantBody {
				t.Errorf("body %q", got)
			}
			if got := r.Header.Get("Cache-Control"); got != tc.wantCache {
				t.Errorf("Cache-Control %q", got)
			}
			if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, tc.wantType) {
				t.Errorf("Content-Type %q", got)
			}
		})
	}
}

func TestMissingAssetIs404(t *testing.T) {
	r := get(t, NewHandler(site), http.MethodGet, "/assets/old-hash.js")
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", r.StatusCode)
	}
}

func TestOnlyGetAndHead(t *testing.T) {
	h := NewHandler(site)
	if r := get(t, h, http.MethodHead, "/reports"); r.StatusCode != http.StatusOK {
		t.Errorf("HEAD: %d", r.StatusCode)
	}
	r := get(t, h, http.MethodPost, "/reports")
	if r.StatusCode != http.StatusMethodNotAllowed || r.Header.Get("Allow") != "GET, HEAD" {
		t.Errorf("POST: %d %q", r.StatusCode, r.Header.Get("Allow"))
	}
}

func TestNotBuiltPage(t *testing.T) {
	r := get(t, NewHandler(fstest.MapFS{".gitkeep": {}}), http.MethodGet, "/")
	if r.StatusCode != http.StatusOK || !strings.Contains(body(t, r), "make build") {
		t.Fatalf("status %d", r.StatusCode)
	}
}

func TestEmbeddedHandlerWorks(t *testing.T) {
	// Works both with and without a web build: either the SPA or the "not built" page.
	r := get(t, Handler(), http.MethodGet, "/")
	if r.StatusCode != http.StatusOK || !strings.Contains(body(t, r), "<html") {
		t.Fatalf("status %d", r.StatusCode)
	}
}
