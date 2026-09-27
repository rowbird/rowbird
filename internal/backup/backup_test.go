package backup_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/backup"
	"github.com/rowbird/rowbird/internal/security"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/testenv"
)

func writeBackup(t *testing.T, e *testenv.Env, artifacts string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "b.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := backup.Write(t.Context(), e.Store, f, backup.Options{ArtifactsDir: artifacts, KeyID: "k1", Version: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if m.Schema == 0 || m.KeyID != "k1" || m.WithArtifacts != (artifacts != "") {
		t.Fatalf("manifest %+v", m)
	}
	return path
}

func TestWriteAndRestore(t *testing.T) {
	e := testenv.New(t)
	artifacts := t.TempDir()
	if err := os.MkdirAll(filepath.Join(artifacts, "w", "r"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifacts, "w", "r", "a.csv"), []byte("id\n1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := writeBackup(t, e, artifacts)

	// A later change is undone by the restore.
	if err := e.Store.Settings().Put(e.Ctx, "after_backup", `true`, false); err != nil {
		t.Fatal(err)
	}
	if m, err := backup.ReadManifest(archive); err != nil || m.Version != "1.2.3" {
		t.Fatalf("manifest %+v, %v", m, err)
	}

	dir := t.TempDir()
	db := filepath.Join(dir, "rowbird.db")
	if err := os.WriteFile(db, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	restoredArtifacts := filepath.Join(dir, "artifacts")
	res, err := backup.Restore(t.Context(), archive, backup.RestoreOptions{DatabasePath: db, ArtifactsDir: restoredArtifacts, LatestSchema: 1000, KeyID: "k2"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.KeyMismatch || res.Artifacts != 1 || res.Previous != db+".pre-restore" {
		t.Fatalf("restored %+v", res)
	}
	if b, _ := os.ReadFile(res.Previous); string(b) != "old" {
		t.Fatal("previous database not kept")
	}
	if b, _ := os.ReadFile(filepath.Join(restoredArtifacts, "w", "r", "a.csv")); string(b) != "id\n1\n" {
		t.Fatal("artifact not restored")
	}
	st, err := store.Open(t.Context(), security.Secret("sqlite://"+filepath.ToSlash(db)), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.Settings().Get(e.Ctx, "after_backup"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("restored database has a later change: %v", err)
	}
	if _, err := st.Connections().GetByName(e.Ctx, "shop"); err != nil {
		t.Fatalf("restored database lacks data: %v", err)
	}
}

func TestRestoreRefuses(t *testing.T) {
	e := testenv.New(t)
	archive := writeBackup(t, e, "")
	db := filepath.Join(t.TempDir(), "rowbird.db")
	if _, err := backup.Restore(t.Context(), archive, backup.RestoreOptions{DatabasePath: db, LatestSchema: 1}); !errors.Is(err, backup.ErrNewerBackup) {
		t.Fatalf("newer backup: %v", err)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatal("a refused restore wrote the database")
	}

	notGzip := filepath.Join(t.TempDir(), "x")
	_ = os.WriteFile(notGzip, []byte("hello"), 0o600)
	if _, err := backup.Restore(t.Context(), notGzip, backup.RestoreOptions{DatabasePath: db}); !errors.Is(err, backup.ErrNotABackup) {
		t.Fatalf("not a backup: %v", err)
	}

	evil := filepath.Join(t.TempDir(), "evil.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, body string }{{backup.ManifestName, `{"format":1,"schema_version":1}`}, {"artifacts/../../escape", "x"}} {
		_ = tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o600, Size: int64(len(f.body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(f.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = os.WriteFile(evil, buf.Bytes(), 0o600)
	if _, err := backup.Restore(t.Context(), evil, backup.RestoreOptions{DatabasePath: db}); !errors.Is(err, backup.ErrUnsafeMember) {
		t.Fatalf("unsafe path: %v", err)
	}
}

type recorder struct{ errs []error }

func (r *recorder) BackupFinished(_ context.Context, err error) { r.errs = append(r.errs, err) }

func TestScheduler(t *testing.T) {
	e := testenv.New(t)
	dir := filepath.Join(t.TempDir(), "backups")
	rec := &recorder{}
	s, err := backup.NewScheduler(e.Store, backup.SchedulerConfig{Schedule: "0 3 * * *", Dir: dir, Keep: 2, KeyID: "k"},
		backup.SchedulerOptions{Notifier: rec, Now: e.Clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := s.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		e.Clock.Advance(24 * time.Hour)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	if len(names) != 2 || names[0] != backup.FileName(time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("kept %v", names)
	}
	st := s.Status()
	if st.LastAt == nil || st.LastFile != names[1] || st.LastSize == 0 || st.LastError != "" || st.Destination != "local" {
		t.Fatalf("status %+v", st)
	}
	if len(rec.errs) != 3 || rec.errs[2] != nil {
		t.Fatalf("notifier %v", rec.errs)
	}

	// A new scheduler finds the last backup in the directory.
	again, _ := backup.NewScheduler(e.Store, backup.SchedulerConfig{Schedule: "0 3 * * *", Dir: dir, Keep: 2}, backup.SchedulerOptions{})
	if got := again.Status(); got.LastFile != names[1] {
		t.Fatalf("status after restart %+v", got)
	}

	// A directory that cannot be written fails and says why.
	blocked := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocked, nil, 0o600)
	bad, _ := backup.NewScheduler(e.Store, backup.SchedulerConfig{Schedule: "0 3 * * *", Dir: blocked, Keep: 1}, backup.SchedulerOptions{Notifier: rec})
	if err := bad.RunOnce(t.Context()); err == nil || !strings.Contains(bad.Status().LastError, "backup directory") || rec.errs[3] == nil {
		t.Fatalf("failure: %v, %+v", err, bad.Status())
	}
}
