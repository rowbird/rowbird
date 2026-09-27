package app

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/config"
)

func TestServeAndShutdown(t *testing.T) {
	dataDir := t.TempDir()
	cfg, err := config.Load(config.Options{Environ: func() []string {
		return []string{"ROWBIRD_DATA_DIR=" + dataDir, "ROWBIRD_BASE_URL=http://localhost"}
	}})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	ctx, cancel := context.WithCancel(t.Context())
	a, err := New(ctx, cfg, logger, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "master.key")); err != nil {
		t.Fatalf("master key was not generated: %v", err)
	}
	if !strings.Contains(logs.String(), "back it up") {
		t.Error("no warning about backing up the generated master key")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, ln) }()

	// Ready once the scheduler completed its first tick, which a busy machine can delay.
	var status int
	var body []byte
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		resp, err := http.Get("http://" + ln.Addr().String() + "/health/ready")
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if status = resp.StatusCode; status == http.StatusOK {
			break
		}
	}
	if status != http.StatusOK {
		t.Fatalf("ready: %d %s", status, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
	if _, err := http.Get("http://" + ln.Addr().String() + "/health/live"); err == nil {
		t.Fatal("server still accepts connections after shutdown")
	}
}

func TestNewFailsOnUnreachableStore(t *testing.T) {
	cfg, err := config.Load(config.Options{Environ: func() []string {
		return []string{
			"ROWBIRD_DATA_DIR=" + t.TempDir(),
			"ROWBIRD_DATABASE_URL=postgres://rowbird:hunter2@127.0.0.1:1/rowbird?connect_timeout=1",
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(t.Context(), cfg, slog.New(slog.DiscardHandler), Options{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("error leaks the database password: %v", err)
	}
}
