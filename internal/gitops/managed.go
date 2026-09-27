package gitops

import (
	"context"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/store"
)

// Errors of attaching and detaching.
var (
	ErrNotManaged  = apperr.New(apperr.KindConflict, "gitops.not_managed")
	ErrNotDetached = apperr.New(apperr.KindConflict, "gitops.not_detached")
	ErrUnknownKind = apperr.Invalid(apperr.Field("kind", "validation.invalid_value"))
)

// Security events.
const (
	EventDetached = "gitops_detached"
	EventAttached = "gitops_attached"
)

// SetManaged detaches a GitOps resource (attach false), so the UI can change it and the directory
// leaves it alone, or gives a detached one back to the directory (attach true). kind is
// connection, channel, query or report.
func (pl *Planner) SetManaged(ctx context.Context, p *auth.Principal, kind string, id uuid.UUID, attach bool, meta auth.RequestMeta) error {
	var current string
	switch kind {
	case "connection":
		c, err := pl.store.Connections().Get(ctx, id)
		if err != nil {
			return err
		}
		current = c.ManagedBy
	case "channel":
		c, err := pl.store.Channels().Get(ctx, id)
		if err != nil {
			return err
		}
		current = c.ManagedBy
	case "query":
		q, err := pl.store.Queries().Get(ctx, id)
		if err != nil {
			return err
		}
		current = q.ManagedBy
	case "report":
		r, err := pl.store.Reports().Get(ctx, id)
		if err != nil {
			return err
		}
		current = r.ManagedBy
	default:
		return ErrUnknownKind
	}
	to, event := store.ManagedByDetached, EventDetached
	switch {
	case attach && current != store.ManagedByDetached:
		return ErrNotDetached
	case attach:
		to, event = store.ManagedByGitOps, EventAttached
	case current != store.ManagedByGitOps:
		return ErrNotManaged
	}
	return pl.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := pl.store.SetManagedBy(ctx, kind, id, to); err != nil {
			return err
		}
		return pl.store.SecurityEvents().Record(ctx, &store.SecurityEvent{
			ActorUserID: &p.UserID, Type: event, IP: meta.IP, Meta: map[string]any{"kind": kind, "id": id.String()},
		})
	})
}
