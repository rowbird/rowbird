package api

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/notify"
)

func TestEventStream(t *testing.T) {
	var events *notify.Broker
	ts := newTestServer(t, func(d *Deps) { events = d.Notify.Events() })
	admin := ts.setup()
	ws, err := ts.store.Workspaces().GetBySlug(t.Context(), auth.DefaultWorkspaceSlug)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(ts.handler)
	defer srv.Close()

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/v1/events", nil)
	for _, c := range admin.cookies {
		req.AddCookie(c)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" || res.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("stream: %d %v", res.StatusCode, res.Header)
	}
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	next := func() string {
		t.Helper()
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("stream closed")
			}
			return l
		case <-time.After(2 * time.Second):
			t.Fatal("no line from the stream")
		}
		return ""
	}
	if got := next(); got != "retry: 5000" {
		t.Fatalf("first line %q", got)
	}
	for next() != "" {
	}

	// An event for another user and one for another workspace are not seen; the workspace event is.
	other := uuid.New()
	for events.Subscribers() == 0 {
		time.Sleep(time.Millisecond)
	}
	events.Publish(notify.Event{Type: notify.EventNotificationCreated, WorkspaceID: ws.ID, UserID: &other, Data: map[string]any{"id": "x"}})
	events.Publish(notify.Event{Type: notify.EventRunUpdated, WorkspaceID: uuid.New(), Data: map[string]any{"run_id": "y"}})
	events.Publish(notify.Event{Type: notify.EventRunUpdated, WorkspaceID: ws.ID, Data: map[string]any{"run_id": "r1", "status": "running"}})
	if got := next(); got != "event: run.updated" {
		t.Fatalf("event line %q", got)
	}
	if got := next(); !strings.Contains(got, `"run_id":"r1"`) || !strings.HasPrefix(got, "data: ") {
		t.Fatalf("data line %q", got)
	}

	// Closing the broker (shutdown) ends the stream.
	events.Close()
	for {
		select {
		case _, ok := <-lines:
			if !ok {
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the stream did not end on shutdown")
		}
	}
}

func TestEventStreamNeedsASession(t *testing.T) {
	ts := newTestServer(t)
	ts.setup()
	res := ts.client().do(http.MethodGet, "/api/v1/events", nil)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous stream: %d", res.Code)
	}
}
