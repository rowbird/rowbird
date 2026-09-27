package auth

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/store"
)

// NewAPIKeyInput describes a key to create.
type NewAPIKeyInput struct {
	Name      string
	Scopes    []Scope
	ExpiresAt *time.Time
}

// CreatedAPIKey carries the plaintext key, shown to the admin once.
type CreatedAPIKey struct {
	Key       store.APIKey
	Plaintext string
}

// ListAPIKeys lists the keys of the caller's workspace.
func (s *Service) ListAPIKeys(ctx context.Context, req store.PageRequest) (store.Page[store.APIKey], error) {
	return s.store.APIKeys().List(ctx, req)
}

// CreateAPIKey creates a key owned by the caller.
func (s *Service) CreateAPIKey(ctx context.Context, p *Principal, in NewAPIKeyInput, meta RequestMeta) (*CreatedAPIKey, error) {
	var fe fieldErrors
	name := fe.name("name", in.Name)
	if len(in.Scopes) == 0 {
		fe.add("scopes", CodeRequired)
	}
	scopes := make([]string, 0, len(in.Scopes))
	for _, sc := range in.Scopes {
		if sc.Rank() == 0 {
			fe.add("scopes", CodeInvalidValue)
			break
		}
		if !slices.Contains(scopes, string(sc)) {
			scopes = append(scopes, string(sc))
		}
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(s.clock()) {
		fe.add("expires_at", CodeInvalidValue)
	}
	if err := fe.err(); err != nil {
		return nil, err
	}

	plaintext, prefix, hash := NewAPIKey()
	k := &store.APIKey{UserID: p.UserID, Name: name, Prefix: prefix, KeyHash: hash, Scopes: scopes, ExpiresAt: in.ExpiresAt}
	k.CreatedBy = ptr(p.UserID)
	err := s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.APIKeys().Create(ctx, k); err != nil {
			return err
		}
		return s.record(ctx, &p.UserID, EventAPIKeyCreated, meta.IP, map[string]any{"api_key_id": k.ID.String(), "name": name, "scopes": scopes})
	})
	if err != nil {
		return nil, err
	}
	return &CreatedAPIKey{Key: *k, Plaintext: plaintext}, nil
}

// RevokeAPIKey revokes a key of the caller's workspace.
func (s *Service) RevokeAPIKey(ctx context.Context, p *Principal, id uuid.UUID, meta RequestMeta) (*store.APIKey, error) {
	var out *store.APIKey
	err := s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.APIKeys().Revoke(ctx, id, s.clock()); err != nil {
			return err
		}
		k, err := s.store.APIKeys().Get(ctx, id)
		if err != nil {
			return err
		}
		out = k
		return s.record(ctx, &p.UserID, EventAPIKeyRevoked, meta.IP, map[string]any{"api_key_id": id.String(), "name": k.Name})
	})
	return out, err
}

// ListSecurityEvents lists events of the caller's workspace, newest first.
func (s *Service) ListSecurityEvents(ctx context.Context, f store.SecurityEventFilter, req store.PageRequest) (store.Page[store.SecurityEvent], error) {
	return s.store.SecurityEvents().List(ctx, f, req)
}
