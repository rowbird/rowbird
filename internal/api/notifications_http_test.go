package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/store"
)

func TestNotificationsAPI(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.setup()
	ws, _ := ts.store.Workspaces().GetBySlug(t.Context(), auth.DefaultWorkspaceSlug)
	ctx := store.WithWorkspace(t.Context(), ws.ID)
	var me gen.Me
	admin.do(http.MethodGet, "/api/v1/me", nil).decode(t, &me)

	at := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	for i, typ := range []string{"channel_failing", "channel_recovered"} {
		group := ""
		if i == 0 {
			group = "channel:x:failing"
		}
		n := &store.Notification{
			UserID: me.Id, Type: typ, Severity: store.SeverityError, TitleKey: "notifications." + typ,
			Params: map[string]any{"channel": "ops"}, GroupKey: group, LastAt: at,
		}
		if _, _, err := ts.store.Notifications().Record(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	var page gen.NotificationPage
	res := admin.do(http.MethodGet, "/api/v1/notifications?limit=1", nil)
	res.decode(t, &page)
	if res.Code != http.StatusOK || len(page.Items) != 1 || page.UnreadCount != 2 || page.NextCursor == nil || page.Items[0].Type != "channel_recovered" || page.Items[0].Params["channel"] != "ops" {
		t.Fatalf("list %d %s", res.Code, res.Body)
	}
	var n gen.Notification
	res = admin.do(http.MethodPost, "/api/v1/notifications/"+page.Items[0].Id.String()+"/read", nil)
	res.decode(t, &n)
	if res.Code != http.StatusOK || n.ReadAt == nil {
		t.Fatalf("read %d %s", res.Code, res.Body)
	}
	admin.do(http.MethodGet, "/api/v1/notifications?unread=true", nil).decode(t, &page)
	if len(page.Items) != 1 || page.UnreadCount != 1 || page.Items[0].Type != "channel_failing" {
		t.Fatalf("unread %+v", page)
	}
	var marked gen.MarkedCount
	admin.do(http.MethodPost, "/api/v1/notifications/read-all", nil).decode(t, &marked)
	if marked.Marked != 1 {
		t.Fatalf("marked %d", marked.Marked)
	}
	if res := admin.do(http.MethodPost, "/api/v1/notifications/"+ws.ID.String()+"/read", nil); res.Code != http.StatusNotFound {
		t.Fatalf("unknown notification: %d", res.Code)
	}

	var d gen.Dashboard
	res = admin.do(http.MethodGet, "/api/v1/dashboard", nil)
	res.decode(t, &d)
	if res.Code != http.StatusOK || d.NextRuns == nil || d.FailingChannels == nil || d.SuccessRate7d.Total != 0 || d.Onboarding.HasReport || d.Onboarding.HasDelivery {
		t.Fatalf("dashboard %d %s", res.Code, res.Body)
	}
}
