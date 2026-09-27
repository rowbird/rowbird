package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/secretconfig"
	"github.com/rowbird/rowbird/internal/store"
)

// OIDC sign-in (docs/spec/07-security.md, "Authentication"): the authorization code flow with PKCE
// against one provider per workspace. The provider decides how strongly users authenticate, so an
// OIDC session is not asked for Rowbird's own second factor.

// OIDC settings keys and the associated-data kind of the encrypted client secret.
const (
	SettingOIDC       = "oidc"
	SettingOIDCSecret = "oidc_secret"
	OIDCSecretKind    = "setting:oidc"
)

// OIDC security events.
const (
	EventOIDCLinked      = "oidc_linked"
	EventUserProvisioned = "user_provisioned"
)

// CallbackPath is where the provider sends the browser back; the redirect URI to register is the
// public base URL followed by it.
const CallbackPath = "/api/v1/auth/oidc/callback"

// oidcFlowTTL bounds the time between leaving for the provider and coming back.
const oidcFlowTTL = 10 * time.Minute

// OIDC errors. They reach the login page as a code, never with details.
var (
	ErrOIDCUnavailable  = apperr.New(apperr.KindConflict, "auth.oidc_unavailable")
	ErrOIDCFailed       = apperr.New(apperr.KindUnauthenticated, "auth.oidc_failed")
	ErrOIDCNoAccount    = apperr.New(apperr.KindUnauthenticated, "auth.oidc_no_account")
	ErrOIDCDomain       = apperr.New(apperr.KindUnauthenticated, "auth.oidc_domain_not_allowed")
	ErrOIDCUnverified   = apperr.New(apperr.KindUnauthenticated, "auth.oidc_email_unverified")
	ErrOIDCDiscovery    = apperr.New(apperr.KindUnprocessable, "auth.oidc_discovery_failed")
	defaultOIDCScopes   = []string{oidc.ScopeOpenID, "email", "profile"}
	oidcFlowAssociation = []byte("oidc-flow")
)

// OIDCConfig is the provider configuration of a workspace, without the client secret.
type OIDCConfig struct {
	Enabled        bool       `json:"enabled"`
	Issuer         string     `json:"issuer"`
	ClientID       string     `json:"client_id"`
	Scopes         []string   `json:"scopes"`
	ButtonLabel    string     `json:"button_label"`
	AutoProvision  bool       `json:"auto_provision"`
	DefaultRole    store.Role `json:"default_role"`
	AllowedDomains []string   `json:"allowed_domains"`
}

// OIDCSettings is what the settings page shows: the configuration and whether a secret is stored.
type OIDCSettings struct {
	OIDCConfig
	SecretConfigured bool
	// RedirectURI is the address to register at the provider; empty without a base URL.
	RedirectURI string
}

// OIDCInput replaces the configuration. A nil ClientSecret keeps the stored one; an empty one
// removes it (public clients rely on PKCE alone).
type OIDCInput struct {
	OIDCConfig
	ClientSecret *string
}

func (s *Service) redirectURI() string {
	if s.cfg.BaseURL == "" {
		return ""
	}
	return strings.TrimRight(s.cfg.BaseURL, "/") + CallbackPath
}

func (s *Service) oidcContext(ctx context.Context) context.Context {
	if s.httpClient != nil {
		return oidc.ClientContext(ctx, s.httpClient)
	}
	return ctx
}

// loadOIDC reads the configuration of the workspace in ctx; secret is "" when none is stored.
func (s *Service) loadOIDC(ctx context.Context) (OIDCConfig, string, bool, error) {
	var cfg OIDCConfig
	st, err := s.store.Settings().Get(ctx, SettingOIDC)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return cfg, "", false, nil
	case err != nil:
		return cfg, "", false, err
	}
	if err := json.Unmarshal([]byte(st.Value), &cfg); err != nil {
		return cfg, "", false, fmt.Errorf("auth: oidc setting: %w", err)
	}
	sec, err := s.store.Settings().Get(ctx, SettingOIDCSecret)
	if errors.Is(err, store.ErrNotFound) {
		return cfg, "", false, nil
	}
	if err != nil {
		return cfg, "", false, err
	}
	var enc string
	if err := json.Unmarshal([]byte(sec.Value), &enc); err != nil {
		return cfg, "", false, err
	}
	ws, _ := store.WorkspaceFrom(ctx)
	plain, err := s.keyring.Decrypt(enc, secretconfig.AssociatedData(OIDCSecretKind, ws))
	if err != nil {
		return cfg, "", false, fmt.Errorf("auth: decrypt the oidc client secret: %w", err)
	}
	return cfg, string(plain), true, nil
}

// GetOIDCSettings returns the configuration of the workspace in ctx.
func (s *Service) GetOIDCSettings(ctx context.Context) (OIDCSettings, error) {
	cfg, _, configured, err := s.loadOIDC(ctx)
	if err != nil {
		return OIDCSettings{}, err
	}
	if cfg.Scopes == nil {
		cfg.Scopes = defaultOIDCScopes
	}
	if cfg.DefaultRole == "" {
		cfg.DefaultRole = store.RoleViewer
	}
	return OIDCSettings{OIDCConfig: cfg, SecretConfigured: configured, RedirectURI: s.redirectURI()}, nil
}

func normalizeOIDC(in OIDCConfig) (OIDCConfig, error) {
	var fe fieldErrors
	out := in
	out.Issuer = strings.TrimRight(strings.TrimSpace(in.Issuer), "/")
	out.ClientID = strings.TrimSpace(in.ClientID)
	out.ButtonLabel = strings.TrimSpace(in.ButtonLabel)
	if len([]rune(out.ButtonLabel)) > 60 {
		fe.add("button_label", CodeTooLong)
	}
	if out.Issuer != "" || in.Enabled {
		if u, err := url.Parse(out.Issuer); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			fe.add("issuer", "validation.url")
		}
	}
	if in.Enabled && out.ClientID == "" {
		fe.add("client_id", CodeRequired)
	}
	out.Scopes = nil
	for _, sc := range in.Scopes {
		if sc = strings.TrimSpace(sc); sc != "" && !slices.Contains(out.Scopes, sc) {
			out.Scopes = append(out.Scopes, sc)
		}
	}
	if len(out.Scopes) == 0 {
		out.Scopes = defaultOIDCScopes
	}
	if !slices.Contains(out.Scopes, oidc.ScopeOpenID) {
		out.Scopes = append([]string{oidc.ScopeOpenID}, out.Scopes...)
	}
	if out.DefaultRole == "" {
		out.DefaultRole = store.RoleViewer
	}
	// Provisioned users never become admins by themselves.
	if out.DefaultRole != store.RoleViewer && out.DefaultRole != store.RoleEditor {
		fe.add("default_role", CodeInvalidValue)
	}
	out.AllowedDomains = nil
	for _, d := range in.AllowedDomains {
		d = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "@"))
		if d == "" {
			continue
		}
		if strings.ContainsAny(d, " @/:") || !strings.Contains(d, ".") {
			fe.add("allowed_domains", CodeInvalidValue)
			continue
		}
		if !slices.Contains(out.AllowedDomains, d) {
			out.AllowedDomains = append(out.AllowedDomains, d)
		}
	}
	return out, fe.err()
}

// UpdateOIDCSettings replaces the configuration (admin only, enforced by the API).
func (s *Service) UpdateOIDCSettings(ctx context.Context, p *Principal, in OIDCInput, meta RequestMeta) (OIDCSettings, error) {
	cfg, err := normalizeOIDC(in.OIDCConfig)
	if err != nil {
		return OIDCSettings{}, err
	}
	ws, ok := store.WorkspaceFrom(ctx)
	if !ok {
		return OIDCSettings{}, store.ErrNoWorkspace
	}
	b, _ := json.Marshal(cfg)
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Settings().Put(ctx, SettingOIDC, string(b), false); err != nil {
			return err
		}
		switch {
		case in.ClientSecret == nil:
		case *in.ClientSecret == "":
			if err := s.store.Settings().Delete(ctx, SettingOIDCSecret); err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
		default:
			enc, err := s.keyring.Encrypt([]byte(*in.ClientSecret), secretconfig.AssociatedData(OIDCSecretKind, ws))
			if err != nil {
				return err
			}
			v, _ := json.Marshal(enc)
			if err := s.store.Settings().Put(ctx, SettingOIDCSecret, string(v), true); err != nil {
				return err
			}
		}
		return s.record(ctx, &p.UserID, EventSettingsChanged, meta.IP, map[string]any{"keys": []string{"oidc"}, "enabled": cfg.Enabled, "issuer": cfg.Issuer})
	})
	if err != nil {
		return OIDCSettings{}, err
	}
	return s.GetOIDCSettings(ctx)
}

// TestOIDC reads the provider's discovery document.
func (s *Service) TestOIDC(ctx context.Context, issuer string) error {
	cfg, err := normalizeOIDC(OIDCConfig{Issuer: issuer, Enabled: false})
	if err != nil {
		return err
	}
	if _, err := oidc.NewProvider(s.oidcContext(ctx), cfg.Issuer); err != nil {
		s.logger.InfoContext(ctx, "oidc discovery failed", "issuer", cfg.Issuer, "error", err)
		return ErrOIDCDiscovery
	}
	return nil
}

// OIDCPublic tells the login page whether to offer the provider, and with what label.
func (s *Service) OIDCPublic(ctx context.Context) (enabled bool, label string) {
	if s.cfg.BaseURL == "" {
		return false, ""
	}
	ws, err := s.store.Workspaces().GetBySlug(ctx, DefaultWorkspaceSlug)
	if err != nil {
		return false, ""
	}
	cfg, _, _, err := s.loadOIDC(store.WithWorkspace(ctx, ws.ID))
	if err != nil || !cfg.Enabled {
		return false, ""
	}
	return true, cfg.ButtonLabel
}

// oidcFlow travels in an encrypted cookie between leaving for the provider and coming back.
type oidcFlow struct {
	State    string    `json:"s"`
	Nonce    string    `json:"n"`
	Verifier string    `json:"v"`
	Redirect string    `json:"r"`
	Expires  time.Time `json:"e"`
}

type oidcClient struct {
	cfg      OIDCConfig
	ws       uuid.UUID
	provider *oidc.Provider
	oauth    oauth2.Config
}

func (s *Service) oidcClient(ctx context.Context) (*oidcClient, error) {
	if s.cfg.BaseURL == "" {
		return nil, ErrOIDCUnavailable
	}
	ws, err := s.store.Workspaces().GetBySlug(ctx, DefaultWorkspaceSlug)
	if err != nil {
		return nil, ErrOIDCUnavailable
	}
	cfg, secret, _, err := s.loadOIDC(store.WithWorkspace(ctx, ws.ID))
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, ErrOIDCUnavailable
	}
	provider, err := oidc.NewProvider(s.oidcContext(ctx), cfg.Issuer)
	if err != nil {
		s.logger.WarnContext(ctx, "oidc discovery failed", "issuer", cfg.Issuer, "error", err)
		return nil, ErrOIDCFailed
	}
	return &oidcClient{cfg: cfg, ws: ws.ID, provider: provider, oauth: oauth2.Config{
		ClientID: cfg.ClientID, ClientSecret: secret, Endpoint: provider.Endpoint(), RedirectURL: s.redirectURI(), Scopes: cfg.Scopes,
	}}, nil
}

// flowSecret is a 192-bit random value for the state and the nonce.
func flowSecret() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// BeginOIDC returns the provider's authorization URL and the value of the flow cookie. redirect is
// where to go after signing in (an app path, already checked by the caller).
func (s *Service) BeginOIDC(ctx context.Context, redirect string) (authURL, cookie string, err error) {
	c, err := s.oidcClient(ctx)
	if err != nil {
		return "", "", err
	}
	flow := oidcFlow{State: flowSecret(), Nonce: flowSecret(), Verifier: oauth2.GenerateVerifier(), Redirect: redirect, Expires: s.clock().Add(oidcFlowTTL)}
	b, _ := json.Marshal(flow)
	cookie, err = s.keyring.Encrypt(b, oidcFlowAssociation)
	if err != nil {
		return "", "", err
	}
	return c.oauth.AuthCodeURL(flow.State, oidc.Nonce(flow.Nonce), oauth2.S256ChallengeOption(flow.Verifier)), cookie, nil
}

type oidcClaims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified *bool  `json:"email_verified"`
	Name          string `json:"name"`
}

// FinishOIDC completes the flow from the provider's answer and signs the user in. It returns the
// app path to continue to.
func (s *Service) FinishOIDC(ctx context.Context, cookie, state, code string, meta RequestMeta) (*IssuedSession, string, error) {
	raw, err := s.keyring.Decrypt(cookie, oidcFlowAssociation)
	if err != nil {
		return nil, "", ErrOIDCFailed
	}
	var flow oidcFlow
	if err := json.Unmarshal(raw, &flow); err != nil || !s.clock().Before(flow.Expires) ||
		subtle.ConstantTimeCompare([]byte(flow.State), []byte(state)) != 1 || code == "" {
		return nil, "", ErrOIDCFailed
	}
	c, err := s.oidcClient(ctx)
	if err != nil {
		return nil, "", err
	}
	octx := s.oidcContext(ctx)
	token, err := c.oauth.Exchange(octx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		s.logger.WarnContext(ctx, "oidc code exchange failed", "error", err)
		return nil, "", ErrOIDCFailed
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, "", ErrOIDCFailed
	}
	idToken, err := c.provider.Verifier(&oidc.Config{ClientID: c.cfg.ClientID}).Verify(octx, rawID)
	if err != nil || subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(flow.Nonce)) != 1 {
		s.logger.WarnContext(ctx, "oidc id token rejected", "error", err)
		return nil, "", ErrOIDCFailed
	}
	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil || claims.Subject == "" {
		return nil, "", ErrOIDCFailed
	}
	user, err := s.oidcUser(store.WithWorkspace(ctx, c.ws), c.cfg, idToken.Issuer, claims, meta)
	if err != nil {
		return nil, "", err
	}
	wsCtx, err := s.userWorkspace(ctx, user.ID)
	if err != nil {
		return nil, "", err
	}
	if user.Disabled() {
		_ = s.record(wsCtx, &user.ID, EventLoginFailed, meta.IP, map[string]any{"reason": "disabled", "method": "oidc"})
		return nil, "", ErrOIDCNoAccount
	}
	sess, err := s.completeLogin(ctx, wsCtx, user, meta, "oidc")
	if err != nil {
		return nil, "", err
	}
	return sess, flow.Redirect, nil
}

// oidcUser finds the Rowbird user for an identity: by the identity itself, then by a verified email
// (linking the two), then by provisioning a new user when the workspace allows it.
func (s *Service) oidcUser(wsCtx context.Context, cfg OIDCConfig, issuer string, c oidcClaims, meta RequestMeta) (*store.User, error) {
	verified := c.EmailVerified != nil && *c.EmailVerified
	email := store.NormalizeEmail(c.Email)
	if len(cfg.AllowedDomains) > 0 {
		if !verified || email == "" {
			return nil, ErrOIDCUnverified
		}
		_, domain, _ := strings.Cut(email, "@")
		if !slices.Contains(cfg.AllowedDomains, domain) {
			s.recordAnonymous(wsCtx, EventLoginFailed, meta, map[string]any{"reason": "domain_not_allowed", "method": "oidc", "email": email})
			return nil, ErrOIDCDomain
		}
	}
	u, err := s.store.Users().GetByOIDC(wsCtx, issuer, c.Subject)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if email == "" {
		return nil, ErrOIDCNoAccount
	}
	u, err = s.store.Users().GetByEmail(wsCtx, email)
	switch {
	case err == nil:
		if !verified {
			return nil, ErrOIDCUnverified
		}
		if u.OIDCSubject != nil {
			// Already linked to another identity: never move a link silently.
			return nil, ErrOIDCNoAccount
		}
		u.OIDCIssuer, u.OIDCSubject = &issuer, &c.Subject
		if err := s.store.Users().Update(wsCtx, u, "oidc_issuer", "oidc_subject"); err != nil {
			return nil, err
		}
		_ = s.record(wsCtx, &u.ID, EventOIDCLinked, meta.IP, map[string]any{"issuer": issuer})
		return u, nil
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	if !cfg.AutoProvision {
		s.recordAnonymous(wsCtx, EventLoginFailed, meta, map[string]any{"reason": "no_account", "method": "oidc", "email": email})
		return nil, ErrOIDCNoAccount
	}
	if !verified {
		return nil, ErrOIDCUnverified
	}
	return s.provisionOIDCUser(wsCtx, cfg, issuer, email, c, meta)
}

func (s *Service) provisionOIDCUser(wsCtx context.Context, cfg OIDCConfig, issuer, email string, c oidcClaims, meta RequestMeta) (*store.User, error) {
	settings, err := s.GetSettings(wsCtx)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(c.Name)
	if name == "" || len([]rune(name)) > maxNameLength {
		name, _, _ = strings.Cut(email, "@")
	}
	u := &store.User{Email: email, Name: name, Locale: settings.DefaultLocale, Theme: "system", OIDCIssuer: &issuer, OIDCSubject: &c.Subject}
	err = s.store.RunInTx(wsCtx, func(ctx context.Context) error {
		if err := s.store.Users().Create(ctx, u); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return ErrOIDCNoAccount
			}
			return err
		}
		if err := s.store.Members().Add(ctx, &store.Member{UserID: u.ID, Role: cfg.DefaultRole}); err != nil {
			return err
		}
		return s.record(ctx, nil, EventUserProvisioned, meta.IP, map[string]any{"user_id": u.ID.String(), "email": email, "role": string(cfg.DefaultRole), "issuer": issuer})
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}
