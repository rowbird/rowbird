package store_test

import (
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

func TestMigrateFromScratchIsIdempotent(t *testing.T) {
	for dialect, newURL := range storetest.Dialects() {
		t.Run(string(dialect), func(t *testing.T) {
			s := storetest.Open(t, newURL(t))
			ctx := t.Context()

			before, err := s.MigrationStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if before.UpToDate() || before.Current != 0 {
				t.Fatalf("fresh database reported %+v", before)
			}

			first, err := s.Migrate(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Applied) == 0 || first.To != before.Latest || first.BackupPath != "" {
				t.Fatalf("unexpected first result %+v", first)
			}

			second, err := s.Migrate(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(second.Applied) != 0 {
				t.Fatalf("second run applied %v", second.Applied)
			}
			after, err := s.MigrationStatus(ctx)
			if err != nil || !after.UpToDate() {
				t.Fatalf("status after migrate: %+v, %v", after, err)
			}
		})
	}
}

func TestConcurrentMigrationsAreSerialized(t *testing.T) {
	for dialect, newURL := range storetest.Dialects() {
		t.Run(string(dialect), func(t *testing.T) {
			url := newURL(t)
			stores := []*store.Store{storetest.Open(t, url), storetest.Open(t, url), storetest.Open(t, url)}

			var wg sync.WaitGroup
			errs := make([]error, len(stores))
			for i, s := range stores {
				wg.Go(func() { _, errs[i] = s.Migrate(t.Context()) })
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Errorf("migration %d: %v", i, err)
				}
			}
			status, err := stores[0].MigrationStatus(t.Context())
			if err != nil || !status.UpToDate() {
				t.Fatalf("status %+v, %v", status, err)
			}
		})
	}
}

func TestSQLiteBackupBeforeMigrating(t *testing.T) {
	dir := t.TempDir()
	url := "sqlite://" + dir + "/rowbird.db"
	v1 := fstest.MapFS{
		"sqlite/00001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE a (id INTEGER PRIMARY KEY);\nINSERT INTO a VALUES (7);\n")},
	}
	v2 := fstest.MapFS{
		"sqlite/00001_a.sql": v1["sqlite/00001_a.sql"],
		"sqlite/00002_b.sql": {Data: []byte("-- +goose Up\nCREATE TABLE b (id INTEGER PRIMARY KEY);\n")},
	}

	s := storetest.Open(t, url)
	s.SetMigrations(v1)
	res, err := s.Migrate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.BackupPath != "" {
		t.Fatalf("an empty database was backed up to %s", res.BackupPath)
	}

	s.SetMigrations(v2)
	res, err = s.Migrate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.BackupPath != dir+"/rowbird.db.pre-migrate-v1.bak" || res.From != 1 || res.To != 2 {
		t.Fatalf("unexpected result %+v", res)
	}

	backup := storetest.Open(t, "sqlite://"+res.BackupPath)
	backup.SetMigrations(v2)
	status, err := backup.MigrationStatus(t.Context())
	if err != nil || status.Current != 1 {
		t.Fatalf("backup is not at version 1: %+v, %v", status, err)
	}
	var n int
	if err := backup.DB().QueryRowContext(t.Context(), "SELECT id FROM a").Scan(&n); err != nil || n != 7 {
		t.Fatalf("backup lost data: %d, %v", n, err)
	}

	// Nothing pending: no new backup.
	res, err = s.Migrate(t.Context())
	if err != nil || res.BackupPath != "" {
		t.Fatalf("up-to-date migrate made a backup: %+v, %v", res, err)
	}
	entries, _ := os.ReadDir(dir)
	var backups int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			backups++
		}
	}
	if backups != 1 {
		t.Fatalf("expected exactly one backup file, found %d", backups)
	}
}

func TestMigrateRefusesANewerSchema(t *testing.T) {
	url := "sqlite://" + t.TempDir() + "/rowbird.db"
	v1 := fstest.MapFS{
		"sqlite/00001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE a (id INTEGER PRIMARY KEY);\n")},
	}
	v2 := fstest.MapFS{
		"sqlite/00001_a.sql": v1["sqlite/00001_a.sql"],
		"sqlite/00002_b.sql": {Data: []byte("-- +goose Up\nCREATE TABLE b (id INTEGER PRIMARY KEY);\n")},
	}
	s := storetest.Open(t, url)
	s.SetMigrations(v2)
	if _, err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	// An older binary, which only knows the first migration.
	s.SetMigrations(v1)
	if _, err := s.Migrate(t.Context()); !errors.Is(err, store.ErrSchemaNewer) {
		t.Fatalf("migrate with older migrations: %v", err)
	}
}

// TestEveryTenantTableHasWorkspaceID is the architecture test from ADR-0015: tables outside
// store.GlobalTables must carry workspace_id.
func TestEveryTenantTableHasWorkspaceID(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		columns, err := s.Columns(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(columns) == 0 {
			t.Fatal("no tables found")
		}
		for table, cols := range columns {
			if slices.Contains(store.GlobalTables, table) {
				continue
			}
			if !slices.Contains(cols, "workspace_id") {
				t.Errorf("table %s has no workspace_id column; add it or list the table in store.GlobalTables", table)
			}
		}
	})
}
