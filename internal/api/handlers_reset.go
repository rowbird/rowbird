package api

import (
	"context"

	"github.com/rowbird/rowbird/internal/api/gen"
)

func (h *authHandlers) RequestPasswordReset(ctx context.Context, req gen.RequestPasswordResetRequestObject) (gen.RequestPasswordResetResponseObject, error) {
	if err := h.svc.RequestPasswordReset(ctx, req.Body.Email, requestMetaFrom(ctx)); err != nil {
		return nil, err
	}
	return gen.RequestPasswordReset202Response{}, nil
}

func (h *authHandlers) ConfirmPasswordReset(ctx context.Context, req gen.ConfirmPasswordResetRequestObject) (gen.ConfirmPasswordResetResponseObject, error) {
	if err := h.svc.ConfirmPasswordReset(ctx, req.Body.Token, req.Body.Password, requestMetaFrom(ctx)); err != nil {
		return nil, err
	}
	return gen.ConfirmPasswordReset204Response{}, nil
}
