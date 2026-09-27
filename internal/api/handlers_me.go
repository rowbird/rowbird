package api

import (
	"context"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
)

type meHandlers struct{ svc *auth.Service }

func (h *meHandlers) GetMe(ctx context.Context, _ gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	p := PrincipalFrom(ctx)
	u, err := h.svc.Me(ctx, p)
	if err != nil {
		return nil, err
	}
	return gen.GetMe200JSONResponse(toMe(u, p)), nil
}

func (h *meHandlers) UpdateMe(ctx context.Context, req gen.UpdateMeRequestObject) (gen.UpdateMeResponseObject, error) {
	p := PrincipalFrom(ctx)
	b := req.Body
	patch := auth.ProfilePatch{Version: b.Version, Name: b.Name}
	if b.Locale != nil {
		patch.Locale = ptr(string(*b.Locale))
	}
	if b.Theme != nil {
		patch.Theme = ptr(string(*b.Theme))
	}
	u, err := h.svc.UpdateProfile(ctx, p, patch)
	if err != nil {
		return nil, err
	}
	return gen.UpdateMe200JSONResponse(toMe(u, p)), nil
}

func (h *meHandlers) ChangeMyPassword(ctx context.Context, req gen.ChangeMyPasswordRequestObject) (gen.ChangeMyPasswordResponseObject, error) {
	err := h.svc.ChangePassword(ctx, PrincipalFrom(ctx), req.Body.CurrentPassword, req.Body.NewPassword, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.ChangeMyPassword204Response{}, nil
}

func (h *meHandlers) ListMySessions(ctx context.Context, _ gen.ListMySessionsRequestObject) (gen.ListMySessionsResponseObject, error) {
	p := PrincipalFrom(ctx)
	sessions, err := h.svc.ListSessions(ctx, p)
	if err != nil {
		return nil, err
	}
	out := gen.ListMySessions200JSONResponse{Items: make([]gen.Session, len(sessions))}
	for i, s := range sessions {
		out.Items[i] = gen.Session{
			Id: s.ID, Ip: s.IP, UserAgent: s.UserAgent, CreatedAt: s.CreatedAt,
			LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt, Current: s.ID == p.SessionID,
		}
	}
	return out, nil
}

func (h *meHandlers) RevokeMySession(ctx context.Context, req gen.RevokeMySessionRequestObject) (gen.RevokeMySessionResponseObject, error) {
	if err := h.svc.RevokeSession(ctx, PrincipalFrom(ctx), req.SessionId, requestMetaFrom(ctx)); err != nil {
		return nil, err
	}
	return gen.RevokeMySession204Response{}, nil
}

func (h *meHandlers) BeginTotpSetup(ctx context.Context, _ gen.BeginTotpSetupRequestObject) (gen.BeginTotpSetupResponseObject, error) {
	e, err := h.svc.BeginTOTP(ctx, PrincipalFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.BeginTotpSetup200JSONResponse{Secret: e.Secret, OtpauthUrl: e.URL, QrCode: e.QRCodePNG}, nil
}

func (h *meHandlers) ConfirmTotpSetup(ctx context.Context, req gen.ConfirmTotpSetupRequestObject) (gen.ConfirmTotpSetupResponseObject, error) {
	codes, err := h.svc.ConfirmTOTP(ctx, PrincipalFrom(ctx), req.Body.Code, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.ConfirmTotpSetup200JSONResponse{Codes: codes}, nil
}

func (h *meHandlers) DisableTotp(ctx context.Context, req gen.DisableTotpRequestObject) (gen.DisableTotpResponseObject, error) {
	if err := h.svc.DisableTOTP(ctx, PrincipalFrom(ctx), req.Body.Password, requestMetaFrom(ctx)); err != nil {
		return nil, err
	}
	return gen.DisableTotp204Response{}, nil
}

func (h *meHandlers) RegenerateRecoveryCodes(ctx context.Context, req gen.RegenerateRecoveryCodesRequestObject) (gen.RegenerateRecoveryCodesResponseObject, error) {
	codes, err := h.svc.RegenerateRecoveryCodes(ctx, PrincipalFrom(ctx), req.Body.Password, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.RegenerateRecoveryCodes200JSONResponse{Codes: codes}, nil
}

func ptr[T any](v T) *T { return &v }
