package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/store"
)

func TestBackupAndRestore(t *testing.T) {
	e := newCLIEnv(t)
	svc, st := e.service()
	if _, err := svc.Setup(t.Context(), auth.SetupInput{Email: "admin@example.com", Name: "Admin", Password: "admin passphrase 1", Locale: "en", Timezone: "UTC"}, auth.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	if r := e.run("", "backup"); r.code != 1 || !strings.Contains(r.stderr, "-o") {
		t.Fatalf("without -o: %+v", r)
	}
	// A directory that does not exist yet is created, like /data/backups before the first scheduled backup.
	file := filepath.Join(t.TempDir(), "backups", "rowbird.tar.gz")
	r := e.run("", "backup", "-o", file, "--with-artifacts")
	if r.code != 0 || !strings.Contains(r.stderr, "backup written") {
		t.Fatalf("backup: %+v", r)
	}

	// A server that looks alive blocks the restore.
	if err := st.System().Heartbeat(t.Context(), &store.Instance{ID: "i-1", Hostname: "h", Version: "dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if r := e.run("", "restore", file); r.code != 1 || !strings.Contains(r.stderr, "stop it first") {
		t.Fatalf("restore while running: %+v", r)
	}
	_ = st.Close()
	r = e.run("", "restore", file, "--force")
	if r.code != 0 || !strings.Contains(r.stdout, "restored the backup") || !strings.Contains(r.stdout, ".pre-restore") || strings.Contains(r.stdout, "warning") {
		t.Fatalf("restore: %+v", r)
	}

	// The restored store still signs the admin in.
	svc, _ = e.service()
	login(t, svc, "admin@example.com", "admin passphrase 1")

	if r := e.run("", "restore", filepath.Join(t.TempDir(), "missing")); r.code != 1 {
		t.Fatalf("missing file: %+v", r)
	}
}
