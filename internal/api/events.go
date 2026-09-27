package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/notify"
)

// eventsPing is how often an idle stream sends a comment, so that proxies keep it open.
const eventsPing = 15 * time.Second

// GetEvents streams real-time events (ADR-0010). The strict server hands the response object the
// writer only, so the object carries the request context and the subscription.
func (h *notifyHandlers) GetEvents(ctx context.Context, _ gen.GetEventsRequestObject) (gen.GetEventsResponseObject, error) {
	p := PrincipalFrom(ctx)
	events, stop := h.svc.Events().Subscribe(p.WorkspaceID, p.UserID)
	return eventStream{ctx: ctx, events: events, stop: stop}, nil
}

type eventStream struct {
	ctx    context.Context
	events <-chan notify.Event
	stop   func()
}

func (s eventStream) VisitGetEventsResponse(w http.ResponseWriter) error {
	defer s.stop()
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	// Nginx buffers responses unless told otherwise.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "retry: 5000\n: connected\n\n"); err != nil {
		return nil
	}
	if err := rc.Flush(); err != nil {
		return err
	}
	ping := time.NewTicker(eventsPing)
	defer ping.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return nil
			}
		case e, ok := <-s.events:
			if !ok {
				return nil
			}
			data, err := json.Marshal(e.Data)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data); err != nil {
				return nil
			}
		}
		if err := rc.Flush(); err != nil {
			return nil
		}
	}
}
