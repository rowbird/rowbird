package api

import (
	"context"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/backup"
	"github.com/rowbird/rowbird/internal/maintenance"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/updatecheck"
	"github.com/rowbird/rowbird/internal/version"
)

// SystemInfo reports on the instance for the admin settings pages.
type SystemInfo struct {
	Store          *store.Store
	StorageBackend string
	// Backups is nil when scheduled backups are off.
	Backups interface{ Status() backup.Status }
	// Updates is the update checker; nil reports the check as not allowed.
	Updates interface {
		Status(ctx context.Context) updatecheck.Status
	}
}

type systemHandlers struct{ info *SystemInfo }

func (h *systemHandlers) GetStorageStatus(ctx context.Context, _ gen.GetStorageStatusRequestObject) (gen.GetStorageStatusResponseObject, error) {
	st := h.info.Store
	bytes, err := st.System().ArtifactBytes(ctx)
	if err != nil {
		return nil, err
	}
	last, err := st.Maintenance().JobLastRun(ctx, maintenance.JobRetention)
	if err != nil {
		return nil, err
	}
	out := gen.StorageStatus{
		Backend: gen.StorageStatusBackend(h.info.StorageBackend), ArtifactBytes: bytes, RetentionLastRunAt: last,
		Backup: gen.BackupStatus{Available: st.Dialect() == store.SQLite},
	}
	if h.info.Backups != nil {
		b := h.info.Backups.Status()
		dest := gen.BackupStatusDestination(b.Destination)
		out.Backup = gen.BackupStatus{
			Available: true, Enabled: true, Schedule: &b.Schedule, Destination: &dest, Keep: &b.Keep,
			LastAt: b.LastAt, NextAt: b.NextAt, LastError: &b.LastError,
		}
		if b.LastFile != "" {
			out.Backup.LastFile, out.Backup.LastSizeBytes = &b.LastFile, &b.LastSize
		}
	}
	return gen.GetStorageStatus200JSONResponse(out), nil
}

func (h *systemHandlers) GetAbout(ctx context.Context, _ gen.GetAboutRequestObject) (gen.GetAboutResponseObject, error) {
	v := version.Get()
	out := gen.About{Version: v.Version, Commit: v.Commit, BuildDate: v.Date, GoVersion: v.GoVersion, Platform: v.Platform}
	if h.info != nil && h.info.Updates != nil {
		s := h.info.Updates.Status(ctx)
		out.UpdateCheckAllowed, out.UpdateCheckEnabled, out.UpdateAvailable, out.CheckedAt = s.Allowed, s.Enabled, s.UpdateAvailable, s.CheckedAt
		if s.Latest != "" {
			out.LatestVersion = &s.Latest
		}
		if s.ReleaseURL != "" {
			out.ReleaseUrl = &s.ReleaseURL
		}
	}
	return gen.GetAbout200JSONResponse(out), nil
}
