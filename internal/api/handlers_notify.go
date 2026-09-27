package api

import (
	"context"
	"time"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/notify"
	"github.com/rowbird/rowbird/internal/store"
)

type notifyHandlers struct {
	svc *notify.Service
}

func toAlertTarget(t *notify.Target) *gen.AlertTarget {
	if t == nil {
		return nil
	}
	opts := t.Options
	if opts == nil {
		opts = map[string]any{}
	}
	return &gen.AlertTarget{ChannelId: t.ChannelID, Options: opts}
}

func fromAlertTarget(t *gen.AlertTarget) *notify.Target {
	if t == nil {
		return nil
	}
	return &notify.Target{ChannelID: t.ChannelId, Options: t.Options}
}

func toAlertSettings(s notify.Settings) gen.AlertSettings {
	return gen.AlertSettings{
		Primary: toAlertTarget(s.Primary), Fallback: toAlertTarget(s.Fallback),
		HeartbeatUrlConfigured: s.HeartbeatConfigured, HeartbeatIntervalSeconds: int(s.HeartbeatInterval / time.Second),
		SameDestination: s.SameDestination,
	}
}

func (h *notifyHandlers) GetAlertSettings(ctx context.Context, _ gen.GetAlertSettingsRequestObject) (gen.GetAlertSettingsResponseObject, error) {
	s, err := h.svc.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetAlertSettings200JSONResponse(toAlertSettings(s)), nil
}

func (h *notifyHandlers) UpdateAlertSettings(ctx context.Context, req gen.UpdateAlertSettingsRequestObject) (gen.UpdateAlertSettingsResponseObject, error) {
	b := req.Body
	in := notify.SettingsInput{Primary: fromAlertTarget(b.Primary), Fallback: fromAlertTarget(b.Fallback), HeartbeatURL: b.HeartbeatUrl}
	if b.HeartbeatIntervalSeconds != nil {
		in.HeartbeatInterval = time.Duration(*b.HeartbeatIntervalSeconds) * time.Second
	}
	s, err := h.svc.UpdateSettings(ctx, PrincipalFrom(ctx), in, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.UpdateAlertSettings200JSONResponse(toAlertSettings(s)), nil
}

func toNotification(n *store.Notification) gen.Notification {
	params := n.Params
	if params == nil {
		params = map[string]any{}
	}
	return gen.Notification{
		Id: n.ID, Type: n.Type, Severity: gen.NotificationSeverity(n.Severity), TitleKey: n.TitleKey, Params: params,
		EntityType: n.EntityType, EntityId: n.EntityID, Count: n.Count, FirstAt: n.FirstAt, LastAt: n.LastAt,
		ReadAt: n.ReadAt, ResolvedAt: n.ResolvedAt,
	}
}

func (h *notifyHandlers) ListNotifications(ctx context.Context, req gen.ListNotificationsRequestObject) (gen.ListNotificationsResponseObject, error) {
	p := PrincipalFrom(ctx)
	page, unread, err := h.svc.List(ctx, p.UserID, req.Params.Unread != nil && *req.Params.Unread, pageRequest(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out := gen.ListNotifications200JSONResponse{Items: make([]gen.Notification, len(page.Items)), UnreadCount: unread}
	for i := range page.Items {
		out.Items[i] = toNotification(&page.Items[i])
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return out, nil
}

func (h *notifyHandlers) MarkNotificationRead(ctx context.Context, req gen.MarkNotificationReadRequestObject) (gen.MarkNotificationReadResponseObject, error) {
	n, err := h.svc.MarkRead(ctx, PrincipalFrom(ctx).UserID, req.NotificationId)
	if err != nil {
		return nil, err
	}
	return gen.MarkNotificationRead200JSONResponse(toNotification(n)), nil
}

func (h *notifyHandlers) MarkAllNotificationsRead(ctx context.Context, _ gen.MarkAllNotificationsReadRequestObject) (gen.MarkAllNotificationsReadResponseObject, error) {
	n, err := h.svc.MarkAllRead(ctx, PrincipalFrom(ctx).UserID)
	if err != nil {
		return nil, err
	}
	return gen.MarkAllNotificationsRead200JSONResponse{Marked: n}, nil
}
