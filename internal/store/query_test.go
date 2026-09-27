package store_test

import (
	"errors"
	"testing"

	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

func TestQueries(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctxA := newWorkspace(t, s, "a")
		ctxB := newWorkspace(t, s, "b")
		conn := &store.Connection{Name: "db", Driver: "sqlite", Config: map[string]any{}}
		if err := s.Connections().Create(ctxA, conn); err != nil {
			t.Fatal(err)
		}
		repo := s.Queries()

		q := &store.Query{Title: "Sales", Slug: "sales", ConnectionID: conn.ID}
		if err := repo.Create(ctxA, q); err != nil {
			t.Fatal(err)
		}
		if err := repo.Create(ctxA, &store.Query{Title: "Other", Slug: "sales", ConnectionID: conn.ID}); !errors.Is(err, store.ErrDuplicate) {
			t.Fatalf("duplicate slug: %v", err)
		}

		def := "10"
		for i, sql := range []string{"select 1", "select 2", "select 3"} {
			v := &store.QueryVersion{QueryID: q.ID, SQL: sql, Note: "n", Params: []store.QueryParam{{Name: "limit", Type: "integer", Default: &def}}}
			if err := repo.AddVersion(ctxA, v); err != nil || v.Number != i+1 {
				t.Fatalf("version %d: %d %v", i+1, v.Number, err)
			}
			q.CurrentVersionID = &v.ID
		}
		if err := repo.Update(ctxA, q, "current_version_id"); err != nil {
			t.Fatal(err)
		}

		v2, err := repo.Version(ctxA, q.ID, 2)
		if err != nil || v2.SQL != "select 2" || len(v2.Params) != 1 || *v2.Params[0].Default != "10" {
			t.Fatalf("version 2: %+v %v", v2, err)
		}
		list, _ := repo.Versions(ctxA, q.ID)
		if len(list) != 3 || list[0].Number != 3 || list[0].SQL != "" {
			t.Fatalf("versions %+v", list)
		}
		if _, err := repo.Version(ctxB, q.ID, 1); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("version visible from B: %v", err)
		}
		used, _ := repo.ListByConnection(ctxA, conn.ID)
		if len(used) != 1 {
			t.Fatalf("by connection %d", len(used))
		}

		if err := repo.Delete(ctxB, q.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("B deleted A's query: %v", err)
		}
		if err := repo.Delete(ctxA, q.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Version(ctxA, q.ID, 1); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("versions survived the query")
		}
	})
}
