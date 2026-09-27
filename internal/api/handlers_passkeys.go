package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/store"
)

func toPasskey(pk *store.Passkey) gen.Passkey {
	return gen.Passkey{Id: pk.ID, Name: pk.Name, CreatedAt: pk.CreatedAt, LastUsedAt: pk.LastUsedAt, Synced: auth.PasskeySynced(pk)}
}

func rawJSON(m map[string]any) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}

func options(raw json.RawMessage) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

func optionalPassword(b *gen.OptionalPassword) string {
	if b == nil || b.Password == nil {
		return ""
	}
	return *b.Password
}

func (h *authHandlers) BeginPasskeyLogin(ctx context.Context, _ gen.BeginPasskeyLoginRequestObject) (gen.BeginPasskeyLoginResponseObject, error) {
	c, err := h.svc.BeginPasskeyLogin(ctx)
	if err != nil {
		return nil, err
	}
	return gen.BeginPasskeyLogin200JSONResponse{ChallengeToken: c.Token, Options: options(c.Options)}, nil
}

func (h *authHandlers) PasskeyLogin(ctx context.Context, req gen.PasskeyLoginRequestObject) (gen.PasskeyLoginResponseObject, error) {
	sess, err := h.svc.FinishPasskeyLogin(ctx, req.Body.ChallengeToken, rawJSON(req.Body.Credential), requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	me, err := h.me(ctx, sess.Principal)
	if err != nil {
		return nil, err
	}
	return signedIn{status: http.StatusOK, body: me, session: sess, cookies: h.cookies}, nil
}

func (h *authHandlers) BeginPasskeySecondFactor(ctx context.Context, req gen.BeginPasskeySecondFactorRequestObject) (gen.BeginPasskeySecondFactorResponseObject, error) {
	opts, err := h.svc.BeginPasskeySecondFactor(ctx, req.Body.ChallengeToken)
	if err != nil {
		return nil, err
	}
	return gen.BeginPasskeySecondFactor200JSONResponse{Options: options(opts)}, nil
}

func (h *meHandlers) ListMyPasskeys(ctx context.Context, _ gen.ListMyPasskeysRequestObject) (gen.ListMyPasskeysResponseObject, error) {
	keys, err := h.svc.ListPasskeys(ctx, PrincipalFrom(ctx))
	if err != nil {
		return nil, err
	}
	out := gen.ListMyPasskeys200JSONResponse{Items: make([]gen.Passkey, len(keys))}
	for i := range keys {
		out.Items[i] = toPasskey(&keys[i])
	}
	return out, nil
}

func (h *meHandlers) BeginMyPasskeyRegistration(ctx context.Context, req gen.BeginMyPasskeyRegistrationRequestObject) (gen.BeginMyPasskeyRegistrationResponseObject, error) {
	c, err := h.svc.BeginPasskeyRegistration(ctx, PrincipalFrom(ctx), optionalPassword(req.Body))
	if err != nil {
		return nil, err
	}
	return gen.BeginMyPasskeyRegistration200JSONResponse{ChallengeToken: c.Token, Options: options(c.Options)}, nil
}

func (h *meHandlers) AddMyPasskey(ctx context.Context, req gen.AddMyPasskeyRequestObject) (gen.AddMyPasskeyResponseObject, error) {
	name := ""
	if req.Body.Name != nil {
		name = *req.Body.Name
	}
	pk, err := h.svc.FinishPasskeyRegistration(ctx, PrincipalFrom(ctx), req.Body.ChallengeToken, name, rawJSON(req.Body.Credential), requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.AddMyPasskey201JSONResponse(toPasskey(pk)), nil
}

func (h *meHandlers) RenameMyPasskey(ctx context.Context, req gen.RenameMyPasskeyRequestObject) (gen.RenameMyPasskeyResponseObject, error) {
	pk, err := h.svc.RenamePasskey(ctx, PrincipalFrom(ctx), req.PasskeyId, req.Body.Name)
	if err != nil {
		return nil, err
	}
	return gen.RenameMyPasskey200JSONResponse(toPasskey(pk)), nil
}

func (h *meHandlers) RemoveMyPasskey(ctx context.Context, req gen.RemoveMyPasskeyRequestObject) (gen.RemoveMyPasskeyResponseObject, error) {
	if err := h.svc.RemovePasskey(ctx, PrincipalFrom(ctx), req.PasskeyId, optionalPassword(req.Body), requestMetaFrom(ctx)); err != nil {
		return nil, err
	}
	return gen.RemoveMyPasskey204Response{}, nil
}
