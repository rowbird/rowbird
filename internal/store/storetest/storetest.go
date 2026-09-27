// Package storetest runs store tests against every supported dialect. SQLite always runs; PostgreSQL
// runs in a testcontainers container when tests are built with the integration tag.
package storetest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rowbird/rowbird/internal/security"
	"github.com/rowbird/rowbird/internal/store"
)

// postgresURL returns the URL of a fresh, empty PostgreSQL database. It is nil unless the
// integration build tag is set.
var postgresURL func(t *testing.T) string

// Dialects returns a URL factory per available dialect. Each call yields an empty database.
func Dialects() map[store.Dialect]func(t *testing.T) string {
	d := map[store.Dialect]func(t *testing.T) string{
		store.SQLite: func(t *testing.T) string {
			return "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "rowbird.db"))
		},
	}
	if postgresURL != nil {
		d[store.Postgres] = postgresURL
	}
	return d
}

// Open opens (without migrating) a store on url and closes it when the test ends.
func Open(t *testing.T, url string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), security.Secret(url), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ForEachDialect runs fn as a subtest per dialect with a freshly migrated store.
func ForEachDialect(t *testing.T, fn func(t *testing.T, s *store.Store)) {
	t.Helper()
	for dialect, newURL := range Dialects() {
		t.Run(string(dialect), func(t *testing.T) {
			s := Open(t, newURL(t))
			if _, err := s.Migrate(context.Background()); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			fn(t, s)
		})
	}
}
