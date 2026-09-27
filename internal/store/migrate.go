package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/rowbird/rowbird/internal/store/ids"
)

// MigrationTable is the goose version table.
const MigrationTable = "rowbird_schema_migrations"

// migrationLockTable backs the SQLite migration lock (ADR-0016).
const migrationLockTable = "rowbird_migration_lock"

// MigrationStatus describes how the database compares to the embedded migrations.
type MigrationStatus struct {
	Current int64
	Latest  int64
}

// ErrSchemaNewer means the database was migrated by a newer Rowbird. Running an older binary on it
// could corrupt data, and there are no automatic downgrades (docs/spec/08-operations.md, "Upgrades").
var ErrSchemaNewer = errors.New("store: the database schema is newer than this version of Rowbird")

// UpToDate reports whether every embedded migration has been applied.
func (m MigrationStatus) UpToDate() bool { return m.Current >= m.Latest }

// MigrateResult summarizes a Migrate call.
type MigrateResult struct {
	From, To int64
	Applied  []string
	// BackupPath is the SQLite copy taken before migrating, empty when none was needed.
	BackupPath string
}

func (s *Store) provider() (*goose.Provider, error) {
	fsys, err := fs.Sub(s.migrations, string(s.dialect))
	if err != nil {
		return nil, fmt.Errorf("store: migrations for %s: %w", s.dialect, err)
	}
	opts := []goose.ProviderOption{
		goose.WithTableName(MigrationTable),
		goose.WithDisableGlobalRegistry(true),
	}
	var dialect goose.Dialect
	switch s.dialect {
	case Postgres:
		dialect = goose.DialectPostgres
		locker, err := lock.NewPostgresSessionLocker()
		if err != nil {
			return nil, fmt.Errorf("store: migration lock: %w", err)
		}
		opts = append(opts, goose.WithSessionLocker(locker))
	case SQLite:
		dialect = goose.DialectSQLite3
		opts = append(opts, goose.WithLocker(newSQLiteLocker()))
	}
	p, err := goose.NewProvider(dialect, s.db.DB, fsys, opts...)
	if err != nil {
		return nil, fmt.Errorf("store: load migrations: %w", err)
	}
	return p, nil
}

// MigrationStatus compares the database with the embedded migrations without changing anything.
func (s *Store) MigrationStatus(ctx context.Context) (MigrationStatus, error) {
	p, err := s.provider()
	if err != nil {
		return MigrationStatus{}, err
	}
	current, latest, err := p.GetVersions(ctx)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("store: read migration version: %w", err)
	}
	return MigrationStatus{Current: current, Latest: latest}, nil
}

// Migrate applies pending migrations under a database lock. On SQLite, a database that already has
// a schema is copied next to the database file before anything is applied.
func (s *Store) Migrate(ctx context.Context) (*MigrateResult, error) {
	p, err := s.provider()
	if err != nil {
		return nil, err
	}
	current, latest, err := p.GetVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: read migration version: %w", err)
	}
	if current > latest {
		return nil, fmt.Errorf("%w: the database is at version %d and this binary knows up to %d; run the newer Rowbird again or restore a backup from before the upgrade", ErrSchemaNewer, current, latest)
	}
	res := &MigrateResult{From: current, To: current}
	if current >= latest {
		return res, nil
	}

	if s.dialect == SQLite && current > 0 {
		if res.BackupPath, err = s.backupSQLite(ctx, current); err != nil {
			return nil, err
		}
		s.logger.InfoContext(ctx, "backed up the database before migrating", "path", res.BackupPath)
	}

	results, err := p.Up(ctx)
	for _, r := range results {
		if r.Error == nil {
			res.Applied = append(res.Applied, r.Source.Path)
			res.To = r.Source.Version
		}
	}
	if err != nil {
		return res, fmt.Errorf("store: migrate: %w", err)
	}
	if len(res.Applied) > 0 {
		s.logger.InfoContext(ctx, "applied migrations", "from", res.From, "to", res.To, "count", len(res.Applied))
	}
	return res, nil
}

// backupSQLite writes a consistent copy of the database with VACUUM INTO.
func (s *Store) backupSQLite(ctx context.Context, version int64) (string, error) {
	path := fmt.Sprintf("%s.pre-migrate-v%d.bak", s.sqlitePath, version)
	if _, err := os.Stat(path); err == nil {
		path = fmt.Sprintf("%s.pre-migrate-v%d-%s.bak", s.sqlitePath, version, time.Now().UTC().Format("20060102T150405Z"))
	}
	if err := s.SnapshotSQLite(ctx, path); err != nil {
		return "", fmt.Errorf("store: back up database before migrating: %w", err)
	}
	return path, nil
}

// ErrNotSQLite is returned by operations that only exist for a SQLite store.
var ErrNotSQLite = errors.New("store: the internal store is not SQLite")

// SQLitePath is the database file of a SQLite store, "" for Postgres.
func (s *Store) SQLitePath() string { return s.sqlitePath }

// SnapshotSQLite writes a consistent copy of the database to path with VACUUM INTO, without
// stopping writers. path must not exist.
func (s *Store) SnapshotSQLite(ctx context.Context, path string) error {
	if s.dialect != SQLite {
		return ErrNotSQLite
	}
	quoted := "'" + strings.ReplaceAll(path, "'", "''") + "'"
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO "+quoted); err != nil {
		return fmt.Errorf("store: snapshot: %w", err)
	}
	return nil
}

// sqliteLocker implements goose's lock.Locker with a single-row table. A lock older than staleAfter
// is assumed to belong to a crashed process and is taken over.
type sqliteLocker struct {
	owner      string
	staleAfter time.Duration
	retry      time.Duration
	timeout    time.Duration
}

func newSQLiteLocker() *sqliteLocker {
	return &sqliteLocker{
		owner:      ids.New().String(),
		staleAfter: 10 * time.Minute,
		retry:      200 * time.Millisecond,
		timeout:    2 * time.Minute,
	}
}

var errLockTimeout = errors.New("store: timed out waiting for the migration lock")

func (l *sqliteLocker) Lock(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+migrationLockTable+` (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		owner TEXT NOT NULL,
		locked_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: create migration lock table: %w", err)
	}
	deadline := time.Now().Add(l.timeout)
	for {
		nowUnix := time.Now().Unix()
		if _, err := db.ExecContext(ctx, `DELETE FROM `+migrationLockTable+` WHERE locked_at < ?`,
			nowUnix-int64(l.staleAfter.Seconds())); err != nil {
			return fmt.Errorf("store: clear stale migration lock: %w", err)
		}
		res, err := db.ExecContext(ctx, `INSERT INTO `+migrationLockTable+` (id, owner, locked_at) VALUES (1, ?, ?) ON CONFLICT (id) DO NOTHING`, l.owner, nowUnix)
		if err != nil {
			return fmt.Errorf("store: take migration lock: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 1 {
			return nil
		}
		if time.Now().After(deadline) {
			return errLockTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(l.retry):
		}
	}
}

func (l *sqliteLocker) Unlock(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM `+migrationLockTable+` WHERE owner = ?`, l.owner); err != nil {
		return fmt.Errorf("store: release migration lock: %w", err)
	}
	return nil
}
