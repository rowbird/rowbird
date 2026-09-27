package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/rowbird/rowbird/internal/logging"
	"github.com/rowbird/rowbird/internal/schedule"
	"github.com/rowbird/rowbird/internal/storage/s3"
	"github.com/rowbird/rowbird/internal/store"
)

const (
	filePrefix = "rowbird-backup-"
	fileSuffix = ".tar.gz"
	fileTime   = "20060102T150405Z"
)

// FileName is the name of a scheduled backup taken at t.
func FileName(t time.Time) string { return filePrefix + t.UTC().Format(fileTime) + fileSuffix }

func parseFileName(name string) (time.Time, bool) {
	s, ok := strings.CutPrefix(name, filePrefix)
	if !ok {
		return time.Time{}, false
	}
	s, ok = strings.CutSuffix(s, fileSuffix)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(fileTime, s)
	return t, err == nil
}

// Notifier hears how each scheduled backup ended (nil on success).
type Notifier interface {
	BackupFinished(ctx context.Context, err error)
}

// SchedulerConfig configures scheduled backups (ROWBIRD_BACKUP_*).
type SchedulerConfig struct {
	Schedule      string
	Dir           string
	Keep          int
	WithArtifacts bool
	ArtifactsDir  string
	// S3 receives a copy of each backup when it names a bucket.
	S3             *s3.Config
	KeyID, Version string
}

// Status is what the settings page shows about scheduled backups.
type Status struct {
	Enabled     bool
	Schedule    string
	Destination string // local or s3
	Keep        int
	LastAt      *time.Time
	LastFile    string
	LastSize    int64
	LastError   string
	NextAt      *time.Time
}

// Scheduler takes backups on a cron schedule, keeping the newest Keep in the directory (and in the
// bucket).
type Scheduler struct {
	store    *store.Store
	cfg      SchedulerConfig
	sched    *schedule.Schedule
	bucket   *minio.Client
	logger   *slog.Logger
	notifier Notifier
	now      func() time.Time

	mu     sync.Mutex
	status Status
}

// SchedulerOptions carries the scheduler's collaborators.
type SchedulerOptions struct {
	Logger   *slog.Logger
	Notifier Notifier
	Now      func() time.Time
}

// NewScheduler validates the configuration. The store must be SQLite.
func NewScheduler(st *store.Store, cfg SchedulerConfig, opts SchedulerOptions) (*Scheduler, error) {
	if st.Dialect() != store.SQLite {
		return nil, store.ErrNotSQLite
	}
	sched, err := schedule.Parse(cfg.Schedule, "UTC")
	if err != nil {
		return nil, fmt.Errorf("backup schedule: %w", err)
	}
	if cfg.Keep < 1 {
		cfg.Keep = 1
	}
	s := &Scheduler{store: st, cfg: cfg, sched: sched, logger: opts.Logger, notifier: opts.Notifier, now: opts.Now}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.status = Status{Enabled: true, Schedule: cfg.Schedule, Destination: "local", Keep: cfg.Keep}
	if cfg.S3 != nil {
		if s.bucket, err = s3.Client(*cfg.S3); err != nil {
			return nil, fmt.Errorf("backup bucket: %w", err)
		}
		s.status.Destination = "s3"
	}
	// The newest file in the directory is the last backup, across restarts.
	if files, err := s.localFiles(); err == nil && len(files) > 0 {
		newest := files[len(files)-1]
		if t, ok := parseFileName(newest); ok {
			s.status.LastAt, s.status.LastFile = &t, newest
			if info, err := os.Stat(filepath.Join(cfg.Dir, newest)); err == nil {
				s.status.LastSize = info.Size()
			}
		}
	}
	return s, nil
}

// Status returns a copy of the current status.
func (s *Scheduler) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Run takes backups until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	s.logger.InfoContext(ctx, "scheduled backups enabled", "schedule", s.cfg.Schedule, "dir", s.cfg.Dir, "destination", s.status.Destination)
	for {
		next := s.sched.Next(s.now())
		s.mu.Lock()
		s.status.NextAt = &next
		s.mu.Unlock()
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		_ = s.RunOnce(ctx)
	}
}

// RunOnce takes one backup now, prunes old ones and reports the outcome.
func (s *Scheduler) RunOnce(ctx context.Context) error {
	name, size, err := s.take(ctx)
	at := s.now().UTC()
	s.mu.Lock()
	if err != nil {
		s.status.LastError = logging.RedactString(err.Error())
	} else {
		s.status.LastAt, s.status.LastFile, s.status.LastSize, s.status.LastError = &at, name, size, ""
	}
	s.mu.Unlock()
	if err != nil {
		s.logger.ErrorContext(ctx, "scheduled backup failed", "error", err)
	} else {
		s.logger.InfoContext(ctx, "scheduled backup written", "file", name, "bytes", size)
	}
	if s.notifier != nil {
		s.notifier.BackupFinished(ctx, err)
	}
	return err
}

func (s *Scheduler) take(ctx context.Context) (string, int64, error) {
	if err := os.MkdirAll(s.cfg.Dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("backup directory: %w", err)
	}
	name := FileName(s.now())
	final := filepath.Join(s.cfg.Dir, name)
	f, err := os.CreateTemp(s.cfg.Dir, ".partial-*")
	if err != nil {
		return "", 0, fmt.Errorf("backup directory: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	opts := Options{KeyID: s.cfg.KeyID, Version: s.cfg.Version, Now: s.now}
	if s.cfg.WithArtifacts {
		opts.ArtifactsDir = s.cfg.ArtifactsDir
	}
	_, werr := Write(ctx, s.store, f, opts)
	if err := errors.Join(werr, f.Sync(), f.Close()); err != nil {
		return "", 0, err
	}
	if err := os.Rename(f.Name(), final); err != nil {
		return "", 0, err
	}
	info, err := os.Stat(final)
	if err != nil {
		return "", 0, err
	}
	if s.bucket != nil {
		if _, err := s.bucket.FPutObject(ctx, s.cfg.S3.Bucket, s.objectKey(name), final, minio.PutObjectOptions{ContentType: "application/gzip"}); err != nil {
			return name, info.Size(), fmt.Errorf("upload backup to the bucket: %w", err)
		}
	}
	return name, info.Size(), s.prune(ctx)
}

func (s *Scheduler) objectKey(name string) string {
	if p := strings.Trim(s.cfg.S3.Prefix, "/"); p != "" {
		return p + "/" + name
	}
	return name
}

// localFiles lists the scheduled backups in the directory, oldest first.
func (s *Scheduler) localFiles() ([]string, error) {
	entries, err := os.ReadDir(s.cfg.Dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if _, ok := parseFileName(e.Name()); ok && e.Type().IsRegular() {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out, nil
}

func (s *Scheduler) prune(ctx context.Context) error {
	files, err := s.localFiles()
	if err != nil {
		return err
	}
	var errs []error
	for len(files) > s.cfg.Keep {
		if err := os.Remove(filepath.Join(s.cfg.Dir, files[0])); err != nil {
			errs = append(errs, err)
		}
		files = files[1:]
	}
	if s.bucket != nil {
		var keys []string
		prefix := s.objectKey(filePrefix)
		for obj := range s.bucket.ListObjects(ctx, s.cfg.S3.Bucket, minio.ListObjectsOptions{Prefix: prefix}) {
			if obj.Err != nil {
				return errors.Join(append(errs, obj.Err)...)
			}
			if _, ok := parseFileName(obj.Key[strings.LastIndex(obj.Key, "/")+1:]); ok {
				keys = append(keys, obj.Key)
			}
		}
		slices.Sort(keys)
		for len(keys) > s.cfg.Keep {
			if err := s.bucket.RemoveObject(ctx, s.cfg.S3.Bucket, keys[0], minio.RemoveObjectOptions{}); err != nil {
				errs = append(errs, err)
			}
			keys = keys[1:]
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("prune old backups: %w", errors.Join(errs...))
	}
	return nil
}
