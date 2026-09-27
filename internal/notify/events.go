package notify

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/store"
)

// Real-time event types sent to the UI (docs/spec/05-api.md, "SSE events").
const (
	EventRunUpdated          = "run.updated"
	EventReportUpdated       = "report.updated"
	EventChannelHealth       = "channel.health"
	EventNotificationCreated = "notification.created"
)

// subscriberBuffer is how many events a slow subscriber may fall behind before it misses some.
// The UI refetches on every event, so a missed one is repaired by the next.
const subscriberBuffer = 64

// Event is one real-time event. UserID limits it to one user; otherwise every member of the
// workspace receives it.
type Event struct {
	Type        string
	WorkspaceID uuid.UUID
	UserID      *uuid.UUID
	Data        map[string]any
}

// Broker fans events out to the subscribers of this process. Events do not cross instances: with
// several instances the UI's slow polling covers the others (ADR-0024).
type Broker struct {
	mu     sync.Mutex
	subs   map[*subscription]struct{}
	closed bool
}

type subscription struct {
	workspace uuid.UUID
	user      uuid.UUID
	ch        chan Event
}

// NewBroker returns an empty broker.
func NewBroker() *Broker { return &Broker{subs: map[*subscription]struct{}{}} }

// Subscribe returns the events a user of a workspace may see and a function to stop. The channel
// is closed when the subscription ends or the broker closes.
func (b *Broker) Subscribe(workspace, user uuid.UUID) (<-chan Event, func()) {
	sub := &subscription{workspace: workspace, user: user, ch: make(chan Event, subscriberBuffer)}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(sub.ch)
		return sub.ch, func() {}
	}
	b.subs[sub] = struct{}{}
	return sub.ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[sub]; ok {
			delete(b.subs, sub)
			close(sub.ch)
		}
	}
}

// Publish sends an event to its subscribers without waiting: a subscriber whose buffer is full
// misses it.
func (b *Broker) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.subs {
		if sub.workspace != e.WorkspaceID || (e.UserID != nil && *e.UserID != sub.user) {
			continue
		}
		select {
		case sub.ch <- e:
		default:
		}
	}
}

// Subscribers counts the open subscriptions.
func (b *Broker) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// Close ends every subscription, so that streaming responses return on shutdown.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for sub := range b.subs {
		delete(b.subs, sub)
		close(sub.ch)
	}
}

// publish sends an event to the workspace in ctx.
func (s *Service) publish(ctx context.Context, typ string, user *uuid.UUID, data map[string]any) {
	ws, ok := store.WorkspaceFrom(ctx)
	if !ok {
		return
	}
	s.events.Publish(Event{Type: typ, WorkspaceID: ws, UserID: user, Data: data})
}

func (s *Service) publishNotification(ctx context.Context, n *store.Notification) {
	u := n.UserID
	s.publish(ctx, EventNotificationCreated, &u, map[string]any{"id": n.ID.String(), "type": n.Type, "count": n.Count})
}

// RunUpdated tells the UI that a run changed status.
func (s *Service) RunUpdated(ctx context.Context, run *store.Run) {
	s.publish(ctx, EventRunUpdated, nil, map[string]any{"run_id": run.ID.String(), "report_id": run.ReportID.String(), "status": run.Status})
}

// ReportUpdated tells the UI that a report changed (paused, resumed, next run).
func (s *Service) ReportUpdated(ctx context.Context, reportID uuid.UUID) {
	s.publish(ctx, EventReportUpdated, nil, map[string]any{"report_id": reportID.String()})
}
