package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/ids"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

func TestWorkspaceRepository(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		repo := s.Workspaces()

		w := &store.Workspace{Name: "Default", Slug: "default"}
		if err := repo.Create(ctx, w); err != nil {
			t.Fatal(err)
		}
		if w.ID.Version() != 7 || w.Version != 1 || w.CreatedAt.IsZero() || w.CreatedAt.Location() != time.UTC {
			t.Fatalf("create did not fill common fields: %+v", w.Base)
		}

		got, err := repo.Get(ctx, w.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "Default" || got.Slug != "default" || got.Version != 1 || !got.CreatedAt.Equal(w.CreatedAt) {
			t.Fatalf("get returned %+v, want %+v", got, w)
		}
		if bySlug, err := repo.GetBySlug(ctx, "default"); err != nil || bySlug.ID != w.ID {
			t.Fatalf("get by slug: %v", err)
		}

		if _, err := repo.Get(ctx, ids.New()); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("missing workspace: got %v", err)
		}
		if err := repo.Create(ctx, &store.Workspace{Name: "Other", Slug: "default"}); !errors.Is(err, store.ErrDuplicate) {
			t.Fatalf("duplicate slug: got %v", err)
		}
	})
}

func TestOptimisticConcurrency(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		repo := s.Workspaces()
		w := &store.Workspace{Name: "A", Slug: "a"}
		if err := repo.Create(ctx, w); err != nil {
			t.Fatal(err)
		}

		first, _ := repo.Get(ctx, w.ID)
		second, _ := repo.Get(ctx, w.ID)

		first.Name = "A1"
		if err := repo.Update(ctx, first); err != nil {
			t.Fatal(err)
		}
		if first.Version != 2 {
			t.Fatalf("version after update = %d, want 2", first.Version)
		}

		second.Name = "A2"
		if err := repo.Update(ctx, second); !errors.Is(err, store.ErrConflict) {
			t.Fatalf("stale update: got %v, want ErrConflict", err)
		}
		if second.Version != 1 {
			t.Fatalf("failed update changed the in-memory version to %d", second.Version)
		}

		stored, _ := repo.Get(ctx, w.ID)
		if stored.Name != "A1" || stored.Version != 2 {
			t.Fatalf("stored %+v", stored)
		}

		ghost := &store.Workspace{Name: "ghost", Slug: "ghost"}
		ghost.ID, ghost.Version = ids.New(), 1
		if err := repo.Update(ctx, ghost); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("update of missing row: got %v, want ErrNotFound", err)
		}
	})
}
