package cli

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type result struct {
	code           int
	stdout, stderr string
}

func run(t *testing.T, ctx context.Context, environ []string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Execute(ctx, args, Env{Stdout: &out, Stderr: &errOut, Environ: func() []string { return environ }})
	return result{code, out.String(), errOut.String()}
}

func TestVersion(t *testing.T) {
	r := run(t, t.Context(), nil, "version")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "rowbird ") {
		t.Fatalf("%+v", r)
	}
}

func TestMigrate(t *testing.T) {
	env := []string{"ROWBIRD_DATA_DIR=" + t.TempDir()}
	first := run(t, t.Context(), env, "migrate")
	if first.code != 0 || !strings.Contains(first.stdout, "migrated schema from version 0") {
		t.Fatalf("first: %+v", first)
	}
	second := run(t, t.Context(), env, "migrate")
	if second.code != 0 || !strings.Contains(second.stdout, "up to date") {
		t.Fatalf("second: %+v", second)
	}
}

func TestHealthcheck(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   int
	}{{200, 0}, {503, 1}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health/ready" {
				t.Errorf("probed %s", r.URL.Path)
			}
			w.WriteHeader(tc.status)
		}))
		addr := strings.TrimPrefix(srv.URL, "http://")
		r := run(t, t.Context(), []string{"ROWBIRD_LISTEN_ADDR=" + addr}, "healthcheck")
		srv.Close()
		if r.code != tc.code {
			t.Errorf("status %d: exit %d, %+v", tc.status, r.code, r)
		}
	}
	if r := run(t, t.Context(), nil, "healthcheck", "--url", "http://127.0.0.1:1/health/ready"); r.code != 1 {
		t.Errorf("unreachable server: exit %d", r.code)
	}
}

func TestReadyURL(t *testing.T) {
	cases := map[string]string{
		":8080":         "http://127.0.0.1:8080/health/ready",
		"0.0.0.0:9000":  "http://127.0.0.1:9000/health/ready",
		"[::]:9000":     "http://127.0.0.1:9000/health/ready",
		"10.1.2.3:8080": "http://10.1.2.3:8080/health/ready",
		"[::1]:8080":    "http://[::1]:8080/health/ready",
	}
	for addr, want := range cases {
		if got := readyURL(addr); got != want {
			t.Errorf("readyURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestServeRunsUntilCancelled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(t.Context())
	env := []string{"ROWBIRD_DATA_DIR=" + t.TempDir()}
	done := make(chan result, 1)
	go func() { done <- run(t, ctx, env, "--listen-addr", addr) }()

	var ready bool
	for range 100 {
		if r := run(t, t.Context(), nil, "healthcheck", "--url", fmt.Sprintf("http://%s/health/ready", addr)); r.code == 0 {
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	r := <-done
	if !ready {
		t.Fatalf("server never became ready: %+v", r)
	}
	if r.code != 0 {
		t.Fatalf("serve exited with %d: %s", r.code, r.stderr)
	}
}

func TestInvalidConfigurationFailsWithoutLeaking(t *testing.T) {
	r := run(t, t.Context(), []string{"ROWBIRD_DATABASE_URL=mysql://root:hunter2@db/x"}, "migrate")
	if r.code != 1 || !strings.Contains(r.stderr, "database_url") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.stderr+r.stdout, "hunter2") {
		t.Fatal("output leaks the password")
	}
}
