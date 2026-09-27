package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
)

// Who manages a resource (docs/spec/09-config-as-code.md, GitOps mode).
const (
	ManagedByGitOps   = "gitops"
	ManagedByDetached = "gitops_detached"
)

// ErrManaged refuses a change to a resource the GitOps directory owns; an admin can detach it.
var ErrManaged = apperr.New(apperr.KindConflict, "resource.managed_by_gitops")

type managedWritesKey struct{}

// WithManagedWrites lets the GitOps apply change resources it manages.
func WithManagedWrites(ctx context.Context) context.Context {
	return context.WithValue(ctx, managedWritesKey{}, true)
}

// CheckManaged returns ErrManaged when managedBy is GitOps and ctx is not the GitOps apply.
func CheckManaged(ctx context.Context, managedBy string) error {
	if managedBy != ManagedByGitOps {
		return nil
	}
	if ok, _ := ctx.Value(managedWritesKey{}).(bool); ok {
		return nil
	}
	return ErrManaged
}

var managedModels = map[string]struct {
	model any
	alias string
}{
	"connection": {(*Connection)(nil), "cn"}, "channel": {(*Channel)(nil), "ch"},
	"query": {(*Query)(nil), "q"}, "report": {(*Report)(nil), "rp"},
}

// SetManagedBy records who manages a resource of kind (connection, channel, query, report).
func (s *Store) SetManagedBy(ctx context.Context, kind string, id uuid.UUID, managedBy string) error {
	m, ok := managedModels[kind]
	if !ok {
		return fmt.Errorf("store: no managed kind %q", kind)
	}
	sc, err := s.Scoped(ctx)
	if err != nil {
		return err
	}
	_, err = sc.NewUpdate(m.model).Set("managed_by = ?", managedBy).Where(m.alias+".id = ?", id).Exec(ctx)
	return mapError(err)
}

// SetOrphan marks or clears a GitOps report whose document left the directory.
func (r *ReportRepo) SetOrphan(ctx context.Context, id uuid.UUID, orphan bool) error {
	sc, err := r.s.Scoped(ctx)
	if err != nil {
		return err
	}
	_, err = sc.NewUpdate((*Report)(nil)).Set("gitops_orphan = ?", orphan).Where("rp.id = ?", id).Exec(ctx)
	return mapError(err)
}
