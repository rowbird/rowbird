package api

import (
	"time"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/store"
)

func toMe(u *store.User, p *auth.Principal) gen.Me {
	restriction := gen.None
	if p.Restriction != auth.RestrictionNone {
		restriction = gen.Restriction(p.Restriction)
	}
	return gen.Me{
		Id:          u.ID,
		Email:       u.Email,
		Name:        u.Name,
		Locale:      gen.Locale(u.Locale),
		Theme:       gen.Theme(u.Theme),
		Role:        gen.Role(p.Role),
		TotpEnabled: u.TOTPEnabled,
		HasPassword: u.PasswordHash != nil,
		Restriction: restriction,
		LastLoginAt: u.LastLoginAt,
		CreatedAt:   u.CreatedAt,
		Version:     u.Version,
	}
}

func toUser(u *auth.UserWithRole, now time.Time) gen.User {
	return gen.User{
		Id:                 u.User.ID,
		Email:              u.User.Email,
		Name:               u.User.Name,
		Role:               gen.Role(u.Role),
		Locale:             gen.Locale(u.User.Locale),
		Disabled:           u.User.Disabled(),
		Locked:             u.User.LockedUntil != nil && now.Before(*u.User.LockedUntil),
		TotpEnabled:        u.User.TOTPEnabled,
		MustChangePassword: u.User.MustChangePassword,
		LastLoginAt:        u.User.LastLoginAt,
		CreatedAt:          u.User.CreatedAt,
		Version:            u.User.Version,
	}
}

func toAPIKey(k *store.APIKey, now time.Time) gen.ApiKey {
	scopes := make([]gen.Scope, len(k.Scopes))
	for i, s := range k.Scopes {
		scopes[i] = gen.Scope(s)
	}
	return gen.ApiKey{
		Id:         k.ID,
		Name:       k.Name,
		Prefix:     k.Prefix,
		Scopes:     scopes,
		Active:     k.Active(now),
		ExpiresAt:  k.ExpiresAt,
		LastUsedAt: k.LastUsedAt,
		RevokedAt:  k.RevokedAt,
		CreatedAt:  k.CreatedAt,
		CreatedBy:  k.CreatedBy,
	}
}

func toSettings(s auth.Settings, linksEnabled bool) gen.Settings {
	return gen.Settings{
		DefaultLocale: gen.Locale(s.DefaultLocale), DefaultTimezone: s.DefaultTimezone, Require2fa: s.Require2FA, LinksEnabled: &linksEnabled,
		RetentionRunsDays: s.RetentionRunsDays, RetentionArtifactsDays: s.RetentionArtifactsDays, UpdateCheck: s.UpdateCheck,
	}
}

func pageRequest(limit *int, cursor *string) store.PageRequest {
	var req store.PageRequest
	if limit != nil {
		req.Limit = *limit
	}
	if cursor != nil {
		req.Cursor = *cursor
	}
	return req
}

func nextCursor(c string) *string {
	if c == "" {
		return nil
	}
	return &c
}
