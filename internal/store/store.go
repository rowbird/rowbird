// Package store is the only gateway to the internal database (ADR-0015). It owns the connection,
// the migrations, the repositories and workspace scoping. Nothing outside this package talks to
// the internal database directly.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/schema"
	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/rowbird/rowbird/internal/logging"
	"github.com/rowbird/rowbird/internal/security"
	"github.com/rowbird/rowbird/internal/store/migrations"
)

// Dialect identifies the SQL flavor of the internal store.
type Dialect string

// Supported dialects.
const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

// Store wraps the internal database connection.
type Store struct {
	db         *bun.DB
	dialect    Dialect
	sqlitePath string
	logger     *slog.Logger
	migrations fs.FS
}

// Open connects to the internal store described by a sqlite:// or postgres:// URL. For SQLite the
// parent directory is created when missing. Open verifies the connection before returning.
func Open(ctx context.Context, databaseURL security.Secret, logger *slog.Logger) (*Store, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	raw := databaseURL.Reveal()
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return nil, errors.New("store: database URL must start with sqlite:// or postgres://")
	}

	s := &Store{logger: logger, migrations: migrations.FS}
	var (
		sqldb *sql.DB
		dial  schema.Dialect
	)
	switch scheme {
	case "sqlite":
		path := filepath.FromSlash(rest)
		if path == "" {
			return nil, errors.New("store: sqlite URL has no file path")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("store: create database directory: %w", err)
		}
		db, err := sql.Open("sqlite", sqliteDSN(path))
		if err != nil {
			return nil, fmt.Errorf("store: open sqlite: %w", err)
		}
		sqldb, dial, s.dialect, s.sqlitePath = db, sqlitedialect.New(), SQLite, path
	case "postgres", "postgresql":
		cfg, err := pgx.ParseConfig(raw)
		if err != nil {
			// pgx errors can quote the URL; never let the password through.
			return nil, fmt.Errorf("store: invalid postgres URL: %s", logging.RedactString(err.Error()))
		}
		sqldb, dial, s.dialect = stdlib.OpenDB(*cfg), pgdialect.New(), Postgres
		sqldb.SetMaxOpenConns(20)
		sqldb.SetMaxIdleConns(5)
		sqldb.SetConnMaxIdleTime(5 * time.Minute)
	default:
		return nil, fmt.Errorf("store: unsupported database scheme %q", scheme)
	}

	s.db = bun.NewDB(sqldb, dial, bun.WithDiscardUnknownColumns())
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.db.PingContext(pingCtx); err != nil {
		_ = s.db.Close()
		return nil, fmt.Errorf("store: connect to %s: %s", s.dialect, logging.RedactString(err.Error()))
	}
	return s, nil
}

// sqliteDSN enables WAL (concurrent readers with one writer), waits on locks instead of failing,
// enforces foreign keys and makes transactions take the write lock up front to avoid deadlocks
// between two read-then-write transactions.
func sqliteDSN(path string) string {
	pragmas := []string{
		"busy_timeout(10000)",
		"journal_mode(WAL)",
		"foreign_keys(1)",
		"synchronous(NORMAL)",
	}
	var b strings.Builder
	b.WriteString("file:")
	b.WriteString(path)
	b.WriteString("?_txlock=immediate&_time_format=sqlite")
	for _, p := range pragmas {
		b.WriteString("&_pragma=")
		b.WriteString(p)
	}
	return b.String()
}

// Dialect reports the SQL flavor of the store.
func (s *Store) Dialect() Dialect { return s.dialect }

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close releases the connection pool.
func (s *Store) Close() error { return s.db.Close() }

type txKey struct{}

// RunInTx runs fn in a transaction. Repositories called with the context passed to fn take part in
// it. Nested calls reuse the outer transaction.
func (s *Store) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(bun.Tx); ok {
		return fn(ctx)
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// conn returns the transaction bound to ctx, or the pool.
func (s *Store) conn(ctx context.Context) bun.IDB {
	if tx, ok := ctx.Value(txKey{}).(bun.Tx); ok {
		return tx
	}
	return s.db
}
