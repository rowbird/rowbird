package store

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Domain errors returned by repositories. The API maps them to stable error codes.
var (
	ErrNotFound    = errors.New("store: not found")
	ErrConflict    = errors.New("store: version conflict")
	ErrDuplicate   = errors.New("store: duplicate value")
	ErrNoWorkspace = errors.New("store: no workspace in context")
)

// mapError converts driver errors into domain errors, keeping the original in the chain.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errors.Join(ErrDuplicate, err)
	}
	var liteErr *sqlite.Error
	if errors.As(err, &liteErr) && (liteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || liteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY) {
		return errors.Join(ErrDuplicate, err)
	}
	return err
}
