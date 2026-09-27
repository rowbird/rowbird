package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
)

// MasterKeyInfo tells the setup wizard where the master key came from.
type MasterKeyInfo struct {
	Source string // env, file or generated
	Path   string
}

type authHandlers struct {
	svc       *auth.Service
	cookies   cookieWriter
	masterKey MasterKeyInfo
}

func (h *authHandlers) GetSetupStatus(ctx context.Context, _ gen.GetSetupStatusRequestObject) (gen.GetSetupStatusResponseObject, error) {
	st, err := h.svc.SetupStatus(ctx)
	if err != nil {
		return nil, err
	}
	mk := gen.MasterKeyInfo{Source: gen.MasterKeyInfoSource(h.masterKey.Source)}
	if st.Required && h.masterKey.Path != "" && h.masterKey.Source == "generated" {
		mk.Path = &h.masterKey.Path
	}
	out := gen.GetSetupStatus200JSONResponse{SetupRequired: st.Required, TokenRequired: st.TokenRequired, MasterKey: mk, PasskeysAvailable: h.svc.PasskeysAvailable()}
	if !st.Required {
		out.PasswordResetAvailable = h.svc.PasswordResetAvailable(ctx)
		enabled, label := h.svc.OIDCPublic(ctx)
		out.OidcEnabled = enabled
		if label != "" {
			out.OidcLabel = &label
		}
	}
	return out, nil
}

func (h *authHandlers) RunSetup(ctx context.Context, req gen.RunSetupRequestObject) (gen.RunSetupResponseObject, error) {
	b := req.Body
	in := auth.SetupInput{Email: b.Email, Name: b.Name, Password: b.Password, Locale: string(b.Locale), Timezone: b.Timezone}
	if b.Token != nil {
		in.Token = *b.Token
	}
	sess, err := h.svc.Setup(ctx, in, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	me, err := h.me(ctx, sess.Principal)
	if err != nil {
		return nil, err
	}
	return signedIn{status: http.StatusCreated, body: me, session: sess, cookies: h.cookies}, nil
}

func (h *authHandlers) Login(ctx context.Context, req gen.LoginRequestObject) (gen.LoginResponseObject, error) {
	res, err := h.svc.Login(ctx, req.Body.Email, req.Body.Password, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	if res.Session == nil {
		token := res.ChallengeToken
		methods := make([]gen.LoginResponseMethods, len(res.Methods))
		for i, m := range res.Methods {
			methods[i] = gen.LoginResponseMethods(m)
		}
		return signedIn{status: http.StatusOK, body: gen.LoginResponse{Status: gen.MfaRequired, ChallengeToken: &token, Methods: &methods}}, nil
	}
	me, err := h.me(ctx, res.Session.Principal)
	if err != nil {
		return nil, err
	}
	return signedIn{status: http.StatusOK, body: gen.LoginResponse{Status: gen.Authenticated, Me: &me}, session: res.Session, cookies: h.cookies}, nil
}

func (h *authHandlers) LoginSecondFactor(ctx context.Context, req gen.LoginSecondFactorRequestObject) (gen.LoginSecondFactorResponseObject, error) {
	var answer auth.SecondFactor
	if req.Body.Code != nil {
		answer.Code = *req.Body.Code
	}
	if req.Body.Passkey != nil {
		answer.Passkey = rawJSON(*req.Body.Passkey)
	}
	sess, err := h.svc.LoginSecondFactor(ctx, req.Body.ChallengeToken, answer, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	me, err := h.me(ctx, sess.Principal)
	if err != nil {
		return nil, err
	}
	return signedIn{status: http.StatusOK, body: me, session: sess, cookies: h.cookies}, nil
}

func (h *authHandlers) Logout(ctx context.Context, _ gen.LogoutRequestObject) (gen.LogoutResponseObject, error) {
	if err := h.svc.Logout(ctx, PrincipalFrom(ctx), requestMetaFrom(ctx)); err != nil {
		return nil, err
	}
	return signedOut{cookies: h.cookies}, nil
}

func (h *authHandlers) me(ctx context.Context, p *auth.Principal) (gen.Me, error) {
	u, err := h.svc.Me(ctx, p)
	if err != nil {
		return gen.Me{}, err
	}
	return toMe(u, p), nil
}

// signedIn writes a JSON body and, when a session was issued, the session and CSRF cookies. The
// generated response types can set only one Set-Cookie header, so this type implements the
// generated visitor interfaces itself.
type signedIn struct {
	status  int
	body    any
	session *auth.IssuedSession
	cookies cookieWriter
}

func (s signedIn) write(w http.ResponseWriter) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(s.body); err != nil {
		return err
	}
	if s.session != nil {
		s.cookies.set(w, s.session)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(s.status)
	_, err := buf.WriteTo(w)
	return err
}

func (s signedIn) VisitRunSetupResponse(w http.ResponseWriter) error          { return s.write(w) }
func (s signedIn) VisitLoginResponse(w http.ResponseWriter) error             { return s.write(w) }
func (s signedIn) VisitLoginSecondFactorResponse(w http.ResponseWriter) error { return s.write(w) }
func (s signedIn) VisitPasskeyLoginResponse(w http.ResponseWriter) error      { return s.write(w) }

// signedOut clears the cookies and answers 204.
type signedOut struct{ cookies cookieWriter }

func (s signedOut) write(w http.ResponseWriter) error {
	s.cookies.clear(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s signedOut) VisitLogoutResponse(w http.ResponseWriter) error { return s.write(w) }

func (s signedOut) VisitRevokeAllMySessionsResponse(w http.ResponseWriter) error { return s.write(w) }
