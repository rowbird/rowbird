package store_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

func TestConnections(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctxA := newWorkspace(t, s, "a")
		ctxB := newWorkspace(t, s, "b")
		repo := s.Connections()
		enc := "v1:abcd:nonce:ct"

		c := &store.Connection{Name: "sales", Driver: "postgres", Config: map[string]any{"host": "db", "port": 5432.0}, SecretsEnc: &enc, QueryTimeoutSeconds: 60, MaxRows: 100000}
		if err := repo.Create(ctxA, c); err != nil {
			t.Fatal(err)
		}
		if err := repo.Create(ctxA, &store.Connection{Name: "sales", Driver: "mysql", Config: map[string]any{}}); !errors.Is(err, store.ErrDuplicate) {
			t.Fatalf("duplicate name: %v", err)
		}
		if err := repo.Create(ctxB, &store.Connection{Name: "sales", Driver: "mysql", Config: map[string]any{}}); err != nil {
			t.Fatalf("same name in another workspace: %v", err)
		}

		got, err := repo.Get(ctxA, c.ID)
		if err != nil || got.Config["host"] != "db" || got.Status != store.ConnectionUnknown || got.SecretsEnc == nil || len(got.AIExcludedTables) != 0 {
			t.Fatalf("get: %+v %v", got, err)
		}
		if _, err := repo.Get(ctxB, c.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("visible from B: %v", err)
		}

		now := time.Now().UTC().Truncate(time.Microsecond)
		yes := true
		if err := repo.SetHealth(ctxA, c.ID, store.ConnectionHealth{Status: store.ConnectionOK, ServerVersion: "PostgreSQL 17", HasWritePermission: &yes, CheckedAt: now}); err != nil {
			t.Fatal(err)
		}
		schema := json.RawMessage(`{"tables":[{"name":"orders","kind":"table","columns":[]}]}`)
		if err := repo.SetSchemaCache(ctxA, c.ID, schema, now); err != nil {
			t.Fatal(err)
		}
		got, _ = repo.Get(ctxA, c.ID)
		if got.Status != store.ConnectionOK || got.HasWritePermission == nil || !*got.HasWritePermission || got.Version != 1 {
			t.Fatalf("health: %+v", got)
		}
		var decoded map[string]any
		if err := json.Unmarshal(got.SchemaCache, &decoded); err != nil || decoded["tables"] == nil {
			t.Fatalf("schema cache: %s %v", got.SchemaCache, err)
		}
		if err := repo.SetHealth(ctxA, c.ID, store.ConnectionHealth{Status: store.ConnectionError, LastError: "connection.timeout", CheckedAt: now}); err != nil {
			t.Fatal(err)
		}
		got, _ = repo.Get(ctxA, c.ID)
		if got.Status != store.ConnectionError || got.ServerVersion != "PostgreSQL 17" || got.LastError != "connection.timeout" {
			t.Fatalf("error health keeps the last good version: %+v", got)
		}

		got.AIExcludedTables = []string{"public.salaries"}
		got.AllowMultiStatement = true
		if err := repo.Update(ctxA, got, "ai_excluded_tables", "allow_multi_statement"); err != nil {
			t.Fatal(err)
		}
		list, err := repo.List(ctxA)
		if err != nil || len(list) != 1 || list[0].SchemaCache != nil || list[0].AIExcludedTables[0] != "public.salaries" || !list[0].AllowMultiStatement {
			t.Fatalf("list: %+v %v", list, err)
		}

		if err := repo.Delete(ctxB, c.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("B deleted A's connection: %v", err)
		}
		if err := repo.Delete(ctxA, c.ID); err != nil {
			t.Fatal(err)
		}
	})
}
