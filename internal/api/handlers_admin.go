package api

import (
	"context"
	"time"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/store"
)

type adminHandlers struct {
	svc          *auth.Service
	cookies      cookieWriter
	linksEnabled bool
}

func (h *adminHandlers) RevokeAllMySessions(ctx context.Context, _ gen.RevokeAllMySessionsRequestObject) (gen.RevokeAllMySessionsResponseObject, error) {
	if err := h.svc.RevokeAllSessions(ctx, PrincipalFrom(ctx), requestMetaFrom(ctx)); err != nil {
		return nil, err
	}
	return signedOut{cookies: h.cookies}, nil
}

func (h *adminHandlers) ListUsers(ctx context.Context, req gen.ListUsersRequestObject) (gen.ListUsersResponseObject, error) {
	page, err := h.svc.ListUsers(ctx, pageRequest(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := gen.ListUsers200JSONResponse{Items: make([]gen.User, len(page.Items)), NextCursor: nextCursor(page.NextCursor)}
	for i := range page.Items {
		out.Items[i] = toUser(&page.Items[i], now)
	}
	return out, nil
}

func (h *adminHandlers) GetUser(ctx context.Context, req gen.GetUserRequestObject) (gen.GetUserResponseObject, error) {
	u, err := h.svc.GetUser(ctx, req.UserId)
	if err != nil {
		return nil, err
	}
	return gen.GetUser200JSONResponse(toUser(u, time.Now())), nil
}

func (h *adminHandlers) CreateUser(ctx context.Context, req gen.CreateUserRequestObject) (gen.CreateUserResponseObject, error) {
	b := req.Body
	in := auth.NewUserInput{Email: b.Email, Name: b.Name, Role: store.Role(b.Role)}
	if b.Locale != nil {
		in.Locale = string(*b.Locale)
	}
	u, temp, err := h.svc.CreateUser(ctx, PrincipalFrom(ctx), in, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.CreateUser201JSONResponse{User: toUser(u, time.Now()), TemporaryPassword: temp}, nil
}

func (h *adminHandlers) UpdateUser(ctx context.Context, req gen.UpdateUserRequestObject) (gen.UpdateUserResponseObject, error) {
	b := req.Body
	patch := auth.UserPatch{Version: b.Version, Name: b.Name, Disabled: b.Disabled}
	if b.Role != nil {
		patch.Role = ptr(store.Role(*b.Role))
	}
	u, err := h.svc.UpdateUser(ctx, PrincipalFrom(ctx), req.UserId, patch, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.UpdateUser200JSONResponse(toUser(u, time.Now())), nil
}

func (h *adminHandlers) ResetUserPassword(ctx context.Context, req gen.ResetUserPasswordRequestObject) (gen.ResetUserPasswordResponseObject, error) {
	temp, err := h.svc.ResetPassword(ctx, PrincipalFrom(ctx), req.UserId, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.ResetUserPassword200JSONResponse{TemporaryPassword: temp}, nil
}

func (h *adminHandlers) DisableUserTotp(ctx context.Context, req gen.DisableUserTotpRequestObject) (gen.DisableUserTotpResponseObject, error) {
	if err := h.svc.AdminDisableTOTP(ctx, PrincipalFrom(ctx), req.UserId, requestMetaFrom(ctx)); err != nil {
		return nil, err
	}
	return gen.DisableUserTotp204Response{}, nil
}

func (h *adminHandlers) ListApiKeys(ctx context.Context, req gen.ListApiKeysRequestObject) (gen.ListApiKeysResponseObject, error) {
	page, err := h.svc.ListAPIKeys(ctx, pageRequest(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := gen.ListApiKeys200JSONResponse{Items: make([]gen.ApiKey, len(page.Items)), NextCursor: nextCursor(page.NextCursor)}
	for i := range page.Items {
		out.Items[i] = toAPIKey(&page.Items[i], now)
	}
	return out, nil
}

func (h *adminHandlers) CreateApiKey(ctx context.Context, req gen.CreateApiKeyRequestObject) (gen.CreateApiKeyResponseObject, error) {
	b := req.Body
	scopes := make([]auth.Scope, len(b.Scopes))
	for i, s := range b.Scopes {
		scopes[i] = auth.Scope(s)
	}
	k, err := h.svc.CreateAPIKey(ctx, PrincipalFrom(ctx), auth.NewAPIKeyInput{Name: b.Name, Scopes: scopes, ExpiresAt: b.ExpiresAt}, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.CreateApiKey201JSONResponse{ApiKey: toAPIKey(&k.Key, time.Now()), Key: k.Plaintext}, nil
}

func (h *adminHandlers) RevokeApiKey(ctx context.Context, req gen.RevokeApiKeyRequestObject) (gen.RevokeApiKeyResponseObject, error) {
	k, err := h.svc.RevokeAPIKey(ctx, PrincipalFrom(ctx), req.ApiKeyId, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.RevokeApiKey200JSONResponse(toAPIKey(k, time.Now())), nil
}

func (h *adminHandlers) GetSettings(ctx context.Context, _ gen.GetSettingsRequestObject) (gen.GetSettingsResponseObject, error) {
	s, err := h.svc.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetSettings200JSONResponse(toSettings(s, h.linksEnabled)), nil
}

func (h *adminHandlers) UpdateSettings(ctx context.Context, req gen.UpdateSettingsRequestObject) (gen.UpdateSettingsResponseObject, error) {
	b := req.Body
	patch := auth.SettingsPatch{
		DefaultTimezone: b.DefaultTimezone, Require2FA: b.Require2fa,
		RetentionRunsDays: b.RetentionRunsDays, RetentionArtifactsDays: b.RetentionArtifactsDays, UpdateCheck: b.UpdateCheck,
	}
	if b.DefaultLocale != nil {
		patch.DefaultLocale = ptr(string(*b.DefaultLocale))
	}
	s, err := h.svc.UpdateSettings(ctx, PrincipalFrom(ctx), patch, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.UpdateSettings200JSONResponse(toSettings(s, h.linksEnabled)), nil
}

func (h *adminHandlers) ListSecurityEvents(ctx context.Context, req gen.ListSecurityEventsRequestObject) (gen.ListSecurityEventsResponseObject, error) {
	f := store.SecurityEventFilter{}
	if req.Params.Type != nil {
		f.Type = *req.Params.Type
	}
	page, err := h.svc.ListSecurityEvents(ctx, f, pageRequest(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out := gen.ListSecurityEvents200JSONResponse{Items: make([]gen.SecurityEvent, len(page.Items)), NextCursor: nextCursor(page.NextCursor)}
	for i, e := range page.Items {
		meta := e.Meta
		if meta == nil {
			meta = map[string]any{}
		}
		out.Items[i] = gen.SecurityEvent{Id: e.ID, Type: e.Type, ActorUserId: e.ActorUserID, Ip: e.IP, Meta: meta, CreatedAt: e.CreatedAt}
	}
	return out, nil
}
