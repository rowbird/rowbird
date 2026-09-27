package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/store"
)

// OIDCCookie carries the state, nonce and PKCE verifier of a single sign-on in progress.
const OIDCCookie = "rowbird_oidc"

const oidcCookiePath = "/api/v1/auth/oidc"

// appPath keeps a redirect inside the app: a path, not a scheme-relative URL, and not the API.
func appPath(p *string) string {
	if p == nil {
		return "/"
	}
	v := *p
	if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.HasPrefix(v, "/\\") || strings.HasPrefix(v, "/api/") {
		return "/"
	}
	return v
}

// oidcRedirect answers a single sign-on step with a redirect, and sets or clears cookies on the way.
type oidcRedirect struct {
	location string
	flow     string // set the flow cookie to this value
	clear    bool   // remove the flow cookie
	session  *auth.IssuedSession
	cookies  cookieWriter
}

func (o oidcRedirect) write(w http.ResponseWriter) error {
	if o.flow != "" || o.clear {
		maxAge := int((10 * time.Minute).Seconds())
		if o.clear {
			maxAge = -1
		}
		// SameSite Lax: the provider sends the browser back with a top-level GET, which carries it.
		http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the public base URL, like the session cookie
			Name: OIDCCookie, Value: o.flow, Path: oidcCookiePath, MaxAge: maxAge,
			HttpOnly: true, Secure: o.cookies.secure, SameSite: http.SameSiteLaxMode,
		})
	}
	if o.session != nil {
		o.cookies.set(w, o.session)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", o.location)
	w.WriteHeader(http.StatusFound)
	return nil
}

func (o oidcRedirect) VisitOidcLoginResponse(w http.ResponseWriter) error    { return o.write(w) }
func (o oidcRedirect) VisitOidcCallbackResponse(w http.ResponseWriter) error { return o.write(w) }

// loginError sends the browser back to the login page with the error code (translated there).
func loginError(err error) string {
	code := "auth.oidc_failed"
	if de, ok := apperr.As(err); ok && strings.HasPrefix(de.Code, "auth.oidc_") {
		code = de.Code
	}
	return "/login?oidc_error=" + url.QueryEscape(code)
}

func (h *authHandlers) OidcLogin(ctx context.Context, req gen.OidcLoginRequestObject) (gen.OidcLoginResponseObject, error) {
	target, flow, err := h.svc.BeginOIDC(ctx, appPath(req.Params.Redirect))
	if err != nil {
		return oidcRedirect{location: loginError(err), cookies: h.cookies}, nil
	}
	return oidcRedirect{location: target, flow: flow, cookies: h.cookies}, nil
}

func (h *authHandlers) OidcCallback(ctx context.Context, req gen.OidcCallbackRequestObject) (gen.OidcCallbackResponseObject, error) {
	p := req.Params
	if p.Error != nil || p.RowbirdOidc == nil || p.Code == nil || p.State == nil {
		return oidcRedirect{location: loginError(auth.ErrOIDCFailed), clear: true, cookies: h.cookies}, nil
	}
	sess, target, err := h.svc.FinishOIDC(ctx, *p.RowbirdOidc, *p.State, *p.Code, requestMetaFrom(ctx))
	if err != nil {
		return oidcRedirect{location: loginError(err), clear: true, cookies: h.cookies}, nil
	}
	return oidcRedirect{location: appPath(&target), clear: true, session: sess, cookies: h.cookies}, nil
}

func toOIDCSettings(s auth.OIDCSettings) gen.OIDCSettings {
	domains := s.AllowedDomains
	if domains == nil {
		domains = []string{}
	}
	return gen.OIDCSettings{
		Enabled: s.Enabled, Issuer: s.Issuer, ClientId: s.ClientID, Scopes: s.Scopes, ButtonLabel: s.ButtonLabel,
		AutoProvision: s.AutoProvision, DefaultRole: gen.OIDCSettingsDefaultRole(s.DefaultRole), AllowedDomains: domains,
		ClientSecretConfigured: s.SecretConfigured, RedirectUri: s.RedirectURI, Available: s.RedirectURI != "",
	}
}

func (h *adminHandlers) GetOIDCSettings(ctx context.Context, _ gen.GetOIDCSettingsRequestObject) (gen.GetOIDCSettingsResponseObject, error) {
	s, err := h.svc.GetOIDCSettings(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetOIDCSettings200JSONResponse(toOIDCSettings(s)), nil
}

func (h *adminHandlers) UpdateOIDCSettings(ctx context.Context, req gen.UpdateOIDCSettingsRequestObject) (gen.UpdateOIDCSettingsResponseObject, error) {
	b := req.Body
	in := auth.OIDCInput{
		OIDCConfig: auth.OIDCConfig{
			Enabled: b.Enabled, Issuer: b.Issuer, ClientID: b.ClientId, Scopes: b.Scopes, ButtonLabel: b.ButtonLabel,
			AutoProvision: b.AutoProvision, DefaultRole: store.Role(b.DefaultRole), AllowedDomains: b.AllowedDomains,
		},
		ClientSecret: b.ClientSecret,
	}
	s, err := h.svc.UpdateOIDCSettings(ctx, PrincipalFrom(ctx), in, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.UpdateOIDCSettings200JSONResponse(toOIDCSettings(s)), nil
}

func (h *adminHandlers) TestOIDCSettings(ctx context.Context, req gen.TestOIDCSettingsRequestObject) (gen.TestOIDCSettingsResponseObject, error) {
	if err := h.svc.TestOIDC(ctx, req.Body.Issuer); err != nil {
		if de, ok := apperr.As(err); ok && de.Kind != apperr.KindInvalid {
			return gen.TestOIDCSettings200JSONResponse{Ok: false, ErrorCode: &de.Code}, nil
		}
		return nil, err
	}
	return gen.TestOIDCSettings200JSONResponse{Ok: true}, nil
}
