package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/security"
)

func TestSQLiteLocker(t *testing.T) {
	s, err := Open(t.Context(), security.Secret("sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "l.db"))), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	db := s.db.DB
	ctx := t.Context()

	first := newSQLiteLocker()
	second := newSQLiteLocker()
	second.timeout, second.retry = 300*time.Millisecond, 50*time.Millisecond

	if err := first.Lock(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := second.Lock(ctx, db); !errors.Is(err, errLockTimeout) {
		t.Fatalf("second lock while held: got %v, want timeout", err)
	}
	// Unlocking with the wrong owner must not release someone else's lock.
	if err := second.Unlock(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := second.Lock(ctx, db); !errors.Is(err, errLockTimeout) {
		t.Fatalf("lock was released by a non-owner: %v", err)
	}
	if err := first.Unlock(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := second.Lock(ctx, db); err != nil {
		t.Fatalf("lock after release: %v", err)
	}

	// A lock older than staleAfter is taken over (the owner is assumed to have crashed).
	if _, err := db.ExecContext(ctx, `UPDATE `+migrationLockTable+` SET locked_at = locked_at - 3600`); err != nil {
		t.Fatal(err)
	}
	third := newSQLiteLocker()
	third.staleAfter = time.Minute
	if err := third.Lock(ctx, db); err != nil {
		t.Fatalf("stale lock was not taken over: %v", err)
	}
}
