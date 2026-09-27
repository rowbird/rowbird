package store_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

func TestNotifications(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctxA := newWorkspace(t, s, "a")
		ctxB := newWorkspace(t, s, "b")
		ana := newUser(t, s, "ana@example.com")
		bruno := newUser(t, s, "bruno@example.com")
		addMember(t, s, ctxA, ana, store.RoleAdmin)
		addMember(t, s, ctxA, bruno, store.RoleEditor)
		if ids, err := s.Members().ActiveAdminIDs(ctxA); err != nil || !slices.Equal(ids, []uuid.UUID{ana.ID}) {
			t.Fatalf("admins %v %v", ids, err)
		}

		at := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
		channel := uuid.New()
		occurrence := func(n int) *store.Notification {
			return &store.Notification{
				UserID: ana.ID, Type: "channel_failing", Severity: store.SeverityError, TitleKey: "notifications.channelFailing.title",
				Params: map[string]any{"error_code": "delivery.unreachable", "n": n}, EntityType: "channel", EntityID: &channel,
				GroupKey: "channel:x:failing", LastAt: at.Add(time.Duration(n) * time.Minute),
			}
		}
		first, created, err := s.Notifications().Record(ctxA, occurrence(0))
		if err != nil || !created || first.Count != 1 {
			t.Fatalf("first %+v %v %v", first, created, err)
		}
		if err := s.Notifications().MarkRead(ctxA, ana.ID, first.ID, at); err != nil {
			t.Fatal(err)
		}
		second, created, err := s.Notifications().Record(ctxA, occurrence(1))
		if err != nil || created || second.ID != first.ID || second.Count != 2 {
			t.Fatalf("second %+v %v %v", second, created, err)
		}
		got, err := s.Notifications().Get(ctxA, ana.ID, first.ID)
		if err != nil || got.Count != 2 || got.ReadAt != nil || !got.FirstAt.Equal(at) || !got.LastAt.Equal(at.Add(time.Minute)) || got.Params["n"] != float64(1) {
			t.Fatalf("grouped %+v %v", got, err)
		}
		if _, err := s.Notifications().Get(ctxA, bruno.ID, first.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("another user's notification: %v", err)
		}
		if _, err := s.Notifications().Get(ctxB, ana.ID, first.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("visible from B: %v", err)
		}
		if open, _ := s.Notifications().HasOpen(ctxA, "channel:x:failing"); !open {
			t.Fatal("group should be open")
		}
		if open, _ := s.Notifications().HasOpen(ctxB, "channel:x:failing"); open {
			t.Fatal("group open in B")
		}

		if ok, err := s.Notifications().Resolve(ctxA, "channel:x:failing", at.Add(time.Hour)); err != nil || !ok {
			t.Fatalf("resolve %v %v", ok, err)
		}
		if ok, _ := s.Notifications().Resolve(ctxA, "channel:x:failing", at.Add(time.Hour)); ok {
			t.Fatal("resolved twice")
		}
		third, created, err := s.Notifications().Record(ctxA, occurrence(2))
		if err != nil || !created || third.ID == first.ID {
			t.Fatalf("after resolve %+v %v %v", third, created, err)
		}
		ungrouped := &store.Notification{UserID: ana.ID, Type: "channel_recovered", Severity: store.SeverityInfo, TitleKey: "k", LastAt: at}
		for range 2 {
			if _, created, err := s.Notifications().Record(ctxA, ungrouped); err != nil || !created {
				t.Fatalf("ungrouped %v %v", created, err)
			}
		}

		if n, _ := s.Notifications().UnreadCount(ctxA, ana.ID); n != 4 {
			t.Fatalf("unread %d", n)
		}
		page, err := s.Notifications().List(ctxA, ana.ID, false, store.PageRequest{Limit: 3})
		if err != nil || len(page.Items) != 3 || page.NextCursor == "" || page.Items[0].Type != "channel_recovered" {
			t.Fatalf("page %+v %v", page, err)
		}
		if err := s.Notifications().MarkRead(ctxA, bruno.ID, third.ID, at); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("mark another user's: %v", err)
		}
		if n, err := s.Notifications().MarkAllRead(ctxA, ana.ID, at); err != nil || n != 4 {
			t.Fatalf("mark all %d %v", n, err)
		}
		unread, _ := s.Notifications().List(ctxA, ana.ID, true, store.PageRequest{})
		if len(unread.Items) != 0 {
			t.Fatalf("unread after mark all: %d", len(unread.Items))
		}
	})
}
