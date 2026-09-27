package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/uptrace/bun"

	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

// widget is a tenant-owned model that only exists in these tests.
type widget struct {
	bun.BaseModel `bun:"table:widgets,alias:wd"`
	store.TenantBase
	Name string `bun:"name,notnull"`
}

func createWidgets(t *testing.T, s *store.Store) {
	t.Helper()
	idType := "TEXT"
	tsType := "TIMESTAMP"
	if s.Dialect() == store.Postgres {
		idType, tsType = "UUID", "TIMESTAMPTZ"
	}
	ddl := `CREATE TABLE widgets (
		id ` + idType + ` PRIMARY KEY, workspace_id ` + idType + ` NOT NULL, name TEXT NOT NULL,
		created_at ` + tsType + ` NOT NULL, updated_at ` + tsType + ` NOT NULL,
		created_by ` + idType + `, version BIGINT NOT NULL)`
	if _, err := s.DB().ExecContext(t.Context(), ddl); err != nil {
		t.Fatal(err)
	}
}

func twoWorkspaces(t *testing.T, s *store.Store) (a, b context.Context) {
	t.Helper()
	wa := &store.Workspace{Name: "A", Slug: "a"}
	wb := &store.Workspace{Name: "B", Slug: "b"}
	for _, w := range []*store.Workspace{wa, wb} {
		if err := s.Workspaces().Create(t.Context(), w); err != nil {
			t.Fatal(err)
		}
	}
	return store.WithWorkspace(t.Context(), wa.ID), store.WithWorkspace(t.Context(), wb.ID)
}

func scoped(t *testing.T, s *store.Store, ctx context.Context) *store.Scoped {
	t.Helper()
	sc, err := s.Scoped(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func TestScopedRequiresWorkspace(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		if _, err := s.Scoped(t.Context()); !errors.Is(err, store.ErrNoWorkspace) {
			t.Fatalf("got %v, want ErrNoWorkspace", err)
		}
		var zero [16]byte
		if _, err := s.Scoped(store.WithWorkspace(t.Context(), zero)); !errors.Is(err, store.ErrNoWorkspace) {
			t.Fatalf("nil workspace: got %v, want ErrNoWorkspace", err)
		}
	})
}

func TestScopedIsolation(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		createWidgets(t, s)
		ctxA, ctxB := twoWorkspaces(t, s)
		scA, scB := scoped(t, s, ctxA), scoped(t, s, ctxB)

		// Insert always writes the builder's workspace, whatever the model says.
		w := &widget{Name: "in A"}
		w.WorkspaceID = scB.WorkspaceID()
		if err := scA.Insert(ctxA, w); err != nil {
			t.Fatal(err)
		}
		if w.WorkspaceID != scA.WorkspaceID() {
			t.Fatalf("insert kept foreign workspace %s", w.WorkspaceID)
		}

		// Select
		var fromA, fromB []widget
		if err := scA.NewSelect(&fromA).Scan(ctxA); err != nil {
			t.Fatal(err)
		}
		if err := scB.NewSelect(&fromB).Scan(ctxB); err != nil {
			t.Fatal(err)
		}
		if len(fromA) != 1 || len(fromB) != 0 {
			t.Fatalf("A sees %d, B sees %d", len(fromA), len(fromB))
		}
		byID := new(widget)
		err := scB.NewSelect(byID).Where("wd.id = ?", w.ID).Scan(ctxB)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("B selected A's widget by id: %v", err)
		}

		// Versioned update from another workspace looks like a missing row.
		stolen := *w
		stolen.Name = "stolen"
		if err := scB.Update(ctxB, &stolen, "name"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("cross-workspace update: got %v, want ErrNotFound", err)
		}
		w.Name = "renamed"
		if err := scA.Update(ctxA, w, "name"); err != nil || w.Version != 2 {
			t.Fatalf("same-workspace update: %v (version %d)", err, w.Version)
		}

		// Raw update and delete builders are filtered too.
		res, err := scB.NewUpdate((*widget)(nil)).Set("name = ?", "x").Where("wd.id = ?", w.ID).Exec(ctxB)
		if n, _ := res.RowsAffected(); err != nil || n != 0 {
			t.Fatalf("cross-workspace raw update touched %d rows: %v", n, err)
		}
		res, err = scB.NewDelete((*widget)(nil)).Where("wd.id = ?", w.ID).Exec(ctxB)
		if n, _ := res.RowsAffected(); err != nil || n != 0 {
			t.Fatalf("cross-workspace delete touched %d rows: %v", n, err)
		}
		res, err = scA.NewDelete((*widget)(nil)).Where("wd.id = ?", w.ID).Exec(ctxA)
		if n, _ := res.RowsAffected(); err != nil || n != 1 {
			t.Fatalf("same-workspace delete touched %d rows: %v", n, err)
		}
	})
}

func TestRunInTx(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		createWidgets(t, s)
		ctxA, _ := twoWorkspaces(t, s)
		boom := errors.New("boom")

		err := s.RunInTx(ctxA, func(ctx context.Context) error {
			if err := scoped(t, s, ctx).Insert(ctx, &widget{Name: "rolled back"}); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
		err = s.RunInTx(ctxA, func(ctx context.Context) error {
			return s.RunInTx(ctx, func(ctx context.Context) error {
				return scoped(t, s, ctx).Insert(ctx, &widget{Name: "kept"})
			})
		})
		if err != nil {
			t.Fatal(err)
		}

		var all []widget
		if err := scoped(t, s, ctxA).NewSelect(&all).Scan(ctxA); err != nil {
			t.Fatal(err)
		}
		if len(all) != 1 || all[0].Name != "kept" {
			t.Fatalf("rows after transactions: %+v", all)
		}
	})
}
