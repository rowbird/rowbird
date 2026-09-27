package store

import (
	"context"
	"io/fs"

	"github.com/uptrace/bun"
)

// SetMigrations replaces the embedded migrations, so tests can simulate an upgrade.
func (s *Store) SetMigrations(fsys fs.FS) { s.migrations = fsys }

// DB exposes the connection to tests that need DDL.
func (s *Store) DB() *bun.DB { return s.db }

// Columns lists the columns of every table in the store.
func (s *Store) Columns(ctx context.Context) (map[string][]string, error) {
	var query string
	switch s.dialect {
	case SQLite:
		query = `SELECT m.name, p.name FROM sqlite_master m JOIN pragma_table_info(m.name) p
			WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%'`
	case Postgres:
		query = `SELECT table_name, column_name FROM information_schema.columns
			WHERE table_schema = current_schema()`
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, err
		}
		out[table] = append(out[table], column)
	}
	return out, rows.Err()
}
