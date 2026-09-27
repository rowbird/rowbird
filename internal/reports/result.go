package reports

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/schedule"
	"github.com/rowbird/rowbird/internal/spool"
	"github.com/rowbird/rowbird/internal/store"
)

// ErrResultUnavailable means the run has no downloadable result (it failed, or its result
// expired).
var ErrResultUnavailable = apperr.New(apperr.KindGone, "run.result_unavailable")

// ResultFile is a run's result rendered by a file formatter. Closing Body removes the temporary
// file behind it.
type ResultFile struct {
	Name        string
	ContentType string
	Size        int64
	Body        io.ReadCloser
}

// RunResult returns a run's result in a file format: the stored artifact when the run's deliveries
// generated that format (any instance can serve it), or else the spooled result rendered in the
// caller's language and the report's time zone (only the instance that ran the report holds it).
func (s *Service) RunResult(ctx context.Context, p *auth.Principal, runID uuid.UUID, formatID string) (*ResultFile, error) {
	if formatID == "" {
		return nil, apperr.Invalid(apperr.Field("format", "validation.required"))
	}
	f, caps, ok := fileFormatter(formatID)
	if !ok {
		return nil, apperr.Invalid(apperr.Field("format", "validation.invalid_value"))
	}
	run, err := s.store.Runs().Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	if file, err := s.artifactFile(ctx, run.ID, formatID); file != nil || err != nil {
		return file, err
	}
	if run.ResultExpiresAt == nil || !run.ResultExpiresAt.After(s.clock()) || s.spoolDir == "" {
		return nil, ErrResultUnavailable
	}
	rp, err := s.store.Reports().Get(ctx, run.ReportID)
	if err != nil {
		return nil, err
	}
	reader, err := spool.Open(spool.Path(s.spoolDir, run.ID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrResultUnavailable
		}
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	loc, err := schedule.LoadLocation(rp.Timezone)
	if err != nil {
		loc = time.UTC
	}
	opts, err := f.ConfigSchema().Validate(map[string]any{})
	if err != nil {
		return nil, err
	}
	started := s.clock()
	if run.StartedAt != nil {
		started = *run.StartedAt
	}
	in := plugin.FormatInput{
		Columns: reader.Columns(), Rows: reader, Truncated: run.Truncated, Locale: s.locale(ctx, p),
		Location: loc, Title: rp.Title, RunID: run.ID.String(), GeneratedAt: started, Options: opts,
	}
	if run.RowCount != nil {
		in.RowCount = *run.RowCount
	}
	tmp, err := os.CreateTemp(s.spoolDir, "download-*.tmp")
	if err != nil {
		return nil, err
	}
	body := &tempFile{File: tmp}
	if _, err := f.Format(ctx, in, tmp); err != nil {
		_ = body.Close()
		return nil, err
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		_ = body.Close()
		return nil, err
	}
	name := rp.Slug + "-" + started.In(loc).Format("2006-01-02-1504") + "." + caps.Extension
	s.logger.InfoContext(ctx, "run result downloaded", "run_id", run.ID, "report_id", rp.ID, "format", formatID, "bytes", size)
	return &ResultFile{Name: name, ContentType: caps.ContentType, Size: size, Body: body}, nil
}

// RunFiles are the files a run's deliveries stored that can still be downloaded.
func (s *Service) RunFiles(ctx context.Context, runID uuid.UUID) ([]store.Artifact, error) {
	arts, err := s.store.Artifacts().ListByRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	out := []store.Artifact{}
	for _, a := range arts {
		if a.DeletedAt == nil && !strings.HasPrefix(a.Format, "inline:") && (a.ExpiresAt == nil || a.ExpiresAt.After(now)) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *Service) artifactFile(ctx context.Context, runID uuid.UUID, formatID string) (*ResultFile, error) {
	if s.storage == nil {
		return nil, nil
	}
	arts, err := s.RunFiles(ctx, runID)
	if err != nil {
		return nil, err
	}
	for _, a := range arts {
		if a.Format != formatID {
			continue
		}
		body, meta, err := s.storage.Get(ctx, a.StorageKey)
		if errors.Is(err, plugin.ErrObjectNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		s.logger.InfoContext(ctx, "run file downloaded", "run_id", runID, "format", formatID, "bytes", meta.Size)
		return &ResultFile{Name: a.FileName, ContentType: a.ContentType, Size: meta.Size, Body: body}, nil
	}
	return nil, nil
}

func fileFormatter(id string) (plugin.Formatter, plugin.FormatterCapabilities, bool) {
	p, ok := plugin.Get(plugin.KindFormatter, id)
	if !ok {
		return nil, plugin.FormatterCapabilities{}, false
	}
	f, ok := p.(plugin.Formatter)
	caps, _ := p.Capabilities().(plugin.FormatterCapabilities)
	return f, caps, ok && caps.Kind == plugin.FormatterFile
}

// locale is the caller's language, English when unknown.
func (s *Service) locale(ctx context.Context, p *auth.Principal) string {
	if p == nil {
		return "en"
	}
	if u, err := s.store.Users().Get(ctx, p.UserID); err == nil && u.Locale != "" {
		return u.Locale
	}
	return "en"
}

type tempFile struct{ *os.File }

func (t *tempFile) Close() error {
	err := t.File.Close()
	_ = os.Remove(t.Name())
	return err
}
