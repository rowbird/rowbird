package notify

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/destination"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/store"
)

// heartbeatCheck is how often the pinger looks at the settings; the interval itself is a setting.
const heartbeatCheck = 5 * time.Second

// heartbeatTimeout bounds one heartbeat request.
const heartbeatTimeout = 10 * time.Second

// Heartbeat calls each workspace's heartbeat URL every heartbeat interval while healthy reports the
// instance healthy (store reachable, scheduler ticking), so that an external monitor notices when
// Rowbird stops working (docs/spec/03-flows.md, section 8). It runs until ctx ends.
func (s *Service) Heartbeat(ctx context.Context, healthy func(context.Context) bool) {
	last := map[uuid.UUID]time.Time{}
	t := time.NewTicker(heartbeatCheck)
	defer t.Stop()
	for {
		s.beat(ctx, healthy, last)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) beat(ctx context.Context, healthy func(context.Context) bool, last map[uuid.UUID]time.Time) {
	workspaces, err := s.store.Workspaces().List(ctx)
	if err != nil {
		return
	}
	checked, ok := false, false
	for _, w := range workspaces {
		wctx := store.WithWorkspace(ctx, w.ID)
		settings, err := s.GetSettings(wctx)
		if err != nil || !settings.HeartbeatConfigured || s.now().Sub(last[w.ID]) < settings.HeartbeatInterval {
			continue
		}
		if !checked {
			checked, ok = true, healthy(ctx)
		}
		if !ok {
			s.logger.WarnContext(ctx, "heartbeat withheld: the instance is not healthy")
			return
		}
		url, err := s.HeartbeatURL(wctx)
		if err != nil || url == "" {
			continue
		}
		last[w.ID] = s.now()
		if code := s.ping(ctx, url); code != "" {
			// The URL is secret (it often carries a token), so only the code is logged.
			s.logger.WarnContext(ctx, "heartbeat failed", "error_code", code)
		}
	}
}

// ping calls url and returns a delivery error code, or "" when it answered with success.
func (s *Service) ping(ctx context.Context, url string) string {
	ctx, cancel := context.WithTimeout(ctx, heartbeatTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return plugin.ErrCodeDeliveryRejected
	}
	// The URL is a workspace setting; the client dials through the network policy.
	if _, _, err := destination.Do(s.http, req); err != nil {
		var de *plugin.DeliveryError
		if errors.As(err, &de) {
			return de.Code
		}
		return plugin.ErrCodeDeliveryFailed
	}
	return ""
}
