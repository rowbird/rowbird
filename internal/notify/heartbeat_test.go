package notify_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/notify"
	"github.com/rowbird/rowbird/internal/store"
)

func TestHeartbeat(t *testing.T) {
	svc, _, _, p := newEnv(t, notify.Options{})
	ctx := store.WithWorkspace(t.Context(), p.WorkspaceID)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Get("status") == "up" {
			hits.Add(1)
		}
	}))
	defer srv.Close()
	u := srv.URL + "/api/push/token?status=up"
	if _, err := svc.UpdateSettings(ctx, p, notify.SettingsInput{HeartbeatURL: &u}, auth.RequestMeta{}); err != nil {
		t.Fatal(err)
	}

	beat := func(healthy bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()
		svc.Heartbeat(ctx, func(context.Context) bool { return healthy })
	}
	beat(false)
	if hits.Load() != 0 {
		t.Fatal("an unhealthy instance sent a heartbeat")
	}
	beat(true)
	if hits.Load() != 1 {
		t.Fatalf("heartbeats %d, want 1 (the interval is a minute)", hits.Load())
	}
}
