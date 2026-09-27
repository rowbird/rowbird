package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/backup"
	"github.com/rowbird/rowbird/internal/updatecheck"
)

type fakeBackups struct{ s backup.Status }

func (f fakeBackups) Status() backup.Status { return f.s }

func TestStorageStatus(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.setup()
	var got gen.StorageStatus
	r := admin.do(http.MethodGet, "/api/v1/system/storage", nil)
	r.decode(t, &got)
	if r.Code != http.StatusOK || got.Backend != "local" || !got.Backup.Available || got.Backup.Enabled || got.RetentionLastRunAt != nil {
		t.Fatalf("%d %+v", r.Code, got)
	}

	last := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	ts = newTestServer(t, func(d *Deps) {
		d.System.Backups = fakeBackups{backup.Status{Enabled: true, Schedule: "0 3 * * *", Destination: "s3", Keep: 7, LastAt: &last, LastFile: "rowbird-backup-x.tar.gz", LastSize: 42}}
	})
	admin = ts.setup()
	r = admin.do(http.MethodGet, "/api/v1/system/storage", nil)
	r.decode(t, &got)
	b := got.Backup
	if !b.Enabled || *b.Schedule != "0 3 * * *" || *b.Destination != "s3" || *b.LastSizeBytes != 42 || !b.LastAt.Equal(last) || *b.LastError != "" {
		t.Fatalf("%+v", b)
	}
}

func TestRetentionSettings(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.setup()
	var s gen.Settings
	admin.do(http.MethodGet, "/api/v1/settings", nil).decode(t, &s)
	if s.RetentionRunsDays != 90 || s.RetentionArtifactsDays != 30 {
		t.Fatalf("defaults %+v", s)
	}
	r := admin.do(http.MethodPatch, "/api/v1/settings", map[string]any{"retention_runs_days": 30, "retention_artifacts_days": 7})
	r.decode(t, &s)
	if r.Code != http.StatusOK || s.RetentionRunsDays != 30 || s.RetentionArtifactsDays != 7 {
		t.Fatalf("%d %+v", r.Code, s)
	}
	r = admin.do(http.MethodPatch, "/api/v1/settings", map[string]any{"retention_runs_days": 0})
	if p := r.problem(t); r.Code != http.StatusBadRequest && r.Code != http.StatusUnprocessableEntity || p.Errors == nil {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
}

type fakeUpdates struct{ s updatecheck.Status }

func (f fakeUpdates) Status(context.Context) updatecheck.Status { return f.s }

func TestAbout(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.setup()
	var got gen.About
	r := admin.do(http.MethodGet, "/api/v1/system/about", nil)
	r.decode(t, &got)
	if r.Code != http.StatusOK || got.Version == "" || got.UpdateCheckAllowed || got.UpdateAvailable {
		t.Fatalf("%d %+v", r.Code, got)
	}
	at := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	ts = newTestServer(t, func(d *Deps) {
		d.System.Updates = fakeUpdates{updatecheck.Status{Allowed: true, Enabled: true, Latest: "v1.3.0", ReleaseURL: "https://example.com/r", CheckedAt: &at, UpdateAvailable: true}}
	})
	admin = ts.setup()
	admin.do(http.MethodGet, "/api/v1/system/about", nil).decode(t, &got)
	if !got.UpdateAvailable || *got.LatestVersion != "v1.3.0" || !got.CheckedAt.Equal(at) {
		t.Fatalf("%+v", got)
	}
	var s gen.Settings
	r = admin.do(http.MethodPatch, "/api/v1/settings", map[string]any{"update_check": false})
	r.decode(t, &s)
	if r.Code != http.StatusOK || s.UpdateCheck {
		t.Fatalf("%d %+v", r.Code, s)
	}
}
