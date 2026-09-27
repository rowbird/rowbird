package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccessLogLevels(t *testing.T) {
	cases := []struct {
		path   string
		status int
		want   string
	}{
		{"/api/v1/reports", http.StatusOK, "level=INFO"},
		{"/health/ready", http.StatusOK, "level=DEBUG"},
		{"/metrics", http.StatusOK, "level=DEBUG"},
		// A failing probe is worth seeing at the usual level.
		{"/health/ready", http.StatusServiceUnavailable, "level=ERROR"},
		{"/metrics", http.StatusUnauthorized, "level=INFO"},
		{"/api/v1/reports", http.StatusInternalServerError, "level=ERROR"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		h := accessLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(c.status) }))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, c.path, nil))
		if !strings.Contains(buf.String(), c.want) {
			t.Errorf("%s %d: %q, want %s", c.path, c.status, buf.String(), c.want)
		}
	}
}
