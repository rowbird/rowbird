package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/store"
)

// inlineFormatters maps a destination's inline target to the formatter that renders it.
var inlineFormatters = map[string]string{
	plugin.InlineHTML:     "html_table",
	plugin.InlineMarkdown: "markdown_table",
	plugin.InlineText:     "text",
}

// InlinePrefix marks artifacts that hold a rendered inline result rather than a file to download.
const InlinePrefix = "inline:"

// FileName is the name of a run's file in a format.
func FileName(rp *store.Report, run *store.Run, loc *time.Location, ext string) string {
	at := run.CreatedAt
	if run.StartedAt != nil {
		at = *run.StartedAt
	}
	if loc == nil {
		loc = time.UTC
	}
	return rp.Slug + "-" + at.In(loc).Format("2006-01-02-1504") + "." + ext
}

func (r *run) urls() (report, run string) {
	if r.e.baseURL == "" {
		return "", ""
	}
	return r.e.baseURL + "/reports/" + r.in.Report.ID.String(), r.e.baseURL + "/runs/" + r.in.Run.ID.String()
}

// message builds what one delivery sends, and the metadata recorded on its attempt.
func (r *run) message(ctx context.Context, d store.Delivery, caps plugin.DestinationCapabilities) (plugin.Message, map[string]any, error) {
	run, rp := r.in.Run, r.in.Report
	reportURL, runURL := r.urls()
	msg := plugin.Message{
		DeliveryID: d.ID.String(), RunID: run.ID.String(),
		Report: plugin.MessageReport{ID: rp.ID.String(), Title: rp.Title, Slug: rp.Slug, URL: reportURL},
		Run: plugin.MessageRun{
			ID: run.ID.String(), URL: runURL, Status: run.Status, Truncated: run.Truncated,
			Condition: r.in.Condition, FinishedAt: run.FinishedAt,
		},
		Status: r.in.Status, Locale: r.locale, Location: r.in.Location, Test: r.in.Test,
	}
	if run.RowCount != nil {
		msg.Run.Rows = *run.RowCount
	}
	if run.DurationMS != nil {
		msg.Run.DurationMS = *run.DurationMS
	}
	if run.StartedAt != nil {
		msg.Run.StartedAt = *run.StartedAt
	}
	if run.ErrorCode != nil {
		msg.Run.ErrorCode = *run.ErrorCode
	}
	meta := map[string]any{"mode": d.Mode}
	if r.in.Status != plugin.StatusUp {
		return msg, meta, nil
	}

	if caps.InlineTarget != "" && (d.Mode == plugin.ModeInline || d.IncludeInlineWithFiles) {
		maxChars := 0
		if caps.MaxTextChars > 0 {
			maxChars = max(caps.MaxTextChars-textReserve, 200)
		}
		inline, err := r.inline(ctx, caps.InlineTarget, d.InlineRowLimit, maxChars, runURL)
		if err != nil {
			return msg, meta, err
		}
		msg.Inline = inline
	}
	if n, _ := d.Options["include_rows"].(int64); n > 0 {
		if err := r.rows(&msg, int(min(n, maxRowsSent))); err != nil {
			return msg, meta, err
		}
	}
	if d.Mode == plugin.ModeInline {
		return msg, meta, nil
	}

	var attached, linked []string
	var total int64
	for _, f := range dedupe(d.Formats) {
		art, err := r.file(ctx, f)
		if err != nil {
			return msg, meta, err
		}
		fits := caps.MaxAttachmentBytes == 0 || total+art.SizeBytes <= caps.MaxAttachmentBytes
		if d.Mode == plugin.ModeAttachment && caps.SupportsAttachments && fits {
			total += art.SizeBytes
			msg.Attachments = append(msg.Attachments, r.attachment(art))
			attached = append(attached, art.FileName)
			continue
		}
		if d.Mode == plugin.ModeAttachment {
			msg.FallbackNote = true
			meta["fallback_to_link"] = true
		}
		link, err := r.link(ctx, d, art)
		if err != nil {
			return msg, meta, err
		}
		msg.Links = append(msg.Links, link)
		linked = append(linked, art.FileName)
	}
	meta["attachments"], meta["links"] = attached, linked
	return msg, meta, nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func (r *run) attachment(a *store.Artifact) plugin.Attachment {
	st := r.e.storage
	return plugin.Attachment{
		Name: a.FileName, Format: a.Format, ContentType: a.ContentType, Size: a.SizeBytes,
		Open: func() (io.ReadCloser, error) {
			rc, _, err := st.Get(context.Background(), a.StorageKey)
			return rc, err
		},
	}
}

// link shares an artifact through a new token.
func (r *run) link(ctx context.Context, d store.Delivery, a *store.Artifact) (plugin.Link, error) {
	if r.e.baseURL == "" {
		return plugin.Link{}, errorf(CodeBaseURLMissing, "delivery: links need ROWBIRD_BASE_URL")
	}
	token, hash, err := newToken()
	if err != nil {
		return plugin.Link{}, err
	}
	ttl := time.Duration(d.LinkExpiresSeconds) * time.Second
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	l := &store.SharedLink{
		ArtifactID: a.ID, RunID: r.in.Run.ID, TokenHash: hash, ExpiresAt: r.e.clock().Add(ttl), RequireLogin: d.LinkRequireLogin,
	}
	if d.ID != uuid.Nil {
		l.DeliveryID = &d.ID
	}
	if err := r.e.store.Links().Create(ctx, l); err != nil {
		return plugin.Link{}, err
	}
	return plugin.Link{Name: a.FileName, Format: a.Format, URL: r.e.baseURL + "/r/" + token, ExpiresAt: l.ExpiresAt}, nil
}

// rows reads the first rows of the result for destinations that send data.
func (r *run) rows(msg *plugin.Message, n int) error {
	rd, err := openSpool(r.e.spoolDir, r.in.Run.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rd.Close() }()
	msg.Columns = rd.Columns()
	for len(msg.Rows) < n && rd.Next() {
		msg.Rows = append(msg.Rows, rd.Row())
	}
	return rd.Err()
}

// inline returns the result rendered for a destination, stored as an artifact so that a resend
// shows the same table after the spool is gone.
func (r *run) inline(ctx context.Context, target string, rows, chars int, link string) (string, error) {
	id, ok := inlineFormatters[target]
	if !ok {
		return "", nil
	}
	key := fmt.Sprintf("%s%s:%d:%d:%t", InlinePrefix, id, rows, chars, link != "")
	art, err := r.artifact(ctx, key, id, func(in *plugin.FormatInput) {
		in.MaxRows, in.MaxChars, in.LinkURL = rows, chars, link
	})
	if err != nil {
		return "", err
	}
	rc, _, err := r.e.storage.Get(ctx, art.StorageKey)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	return string(b), err
}

// file returns the artifact of a file format, generating it on first use.
func (r *run) file(ctx context.Context, formatID string) (*store.Artifact, error) {
	return r.artifact(ctx, formatID, formatID, nil)
}

// artifact finds or generates the artifact named key with formatter formatID.
func (r *run) artifact(ctx context.Context, key, formatID string, tune func(*plugin.FormatInput)) (*store.Artifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.artifacts[key]; ok {
		return a, nil
	}
	existing, err := r.e.store.Artifacts().ListByRun(ctx, r.in.Run.ID)
	if err != nil {
		return nil, err
	}
	for i := range existing {
		if existing[i].Format == key && existing[i].DeletedAt == nil {
			r.artifacts[key] = &existing[i]
			return &existing[i], nil
		}
	}
	a, err := r.generate(ctx, key, formatID, tune)
	if err != nil {
		return nil, err
	}
	r.artifacts[key] = a
	return a, nil
}

func (r *run) generate(ctx context.Context, key, formatID string, tune func(*plugin.FormatInput)) (*store.Artifact, error) {
	p, ok := plugin.Get(plugin.KindFormatter, formatID)
	f, isFormatter := p.(plugin.Formatter)
	if !ok || !isFormatter {
		return nil, errorf(plugin.ErrCodeDeliveryRejected, "delivery: unknown format %q", formatID)
	}
	caps, _ := p.Capabilities().(plugin.FormatterCapabilities)
	rd, err := openSpool(r.e.spoolDir, r.in.Run.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()
	opts, err := f.ConfigSchema().Validate(map[string]any{})
	if err != nil {
		return nil, err
	}
	run := r.in.Run
	in := plugin.FormatInput{
		Columns: rd.Columns(), Rows: rd, Truncated: run.Truncated, Locale: r.locale, Location: r.in.Location,
		Title: r.in.Report.Title, RunID: run.ID.String(), Options: opts, GeneratedAt: run.CreatedAt,
	}
	if run.StartedAt != nil {
		in.GeneratedAt = *run.StartedAt
	}
	if run.RowCount != nil {
		in.RowCount = *run.RowCount
	}
	if tune != nil {
		tune(&in)
	}
	tmp, err := os.CreateTemp(r.e.spoolDir, "artifact-*.tmp")
	if err != nil {
		return nil, err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	hw := &hashingWriter{w: tmp, h: h}
	if _, err := f.Format(ctx, in, hw); err != nil {
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	ext := caps.Extension
	name := FileName(r.in.Report, run, r.in.Location, ext)
	if strings.HasPrefix(key, InlinePrefix) {
		ext = map[string]string{"html_table": "html", "markdown_table": "md", "text": "txt"}[formatID]
		name = strings.NewReplacer(":", "-").Replace(strings.TrimPrefix(key, InlinePrefix)) + "." + ext
	}
	storageKey := fmt.Sprintf("%s/%s/%s/%s", run.WorkspaceID, r.in.Report.ID, run.ID, name)
	meta := plugin.ObjectMeta{ContentType: caps.ContentType, Size: hw.size, FileName: name}
	if err := r.e.storage.Put(ctx, storageKey, tmp, meta); err != nil {
		return nil, fmt.Errorf("delivery: store artifact: %w", err)
	}
	expires := r.e.clock().AddDate(0, 0, r.e.artifactDays(ctx))
	a := &store.Artifact{
		RunID: run.ID, Format: key, ContentType: caps.ContentType, FileName: name, StorageBackend: r.e.backend,
		StorageKey: storageKey, SizeBytes: hw.size, SHA256: hex.EncodeToString(h.Sum(nil)), ExpiresAt: &expires,
	}
	if err := r.e.store.Artifacts().Create(ctx, a); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			// Another delivery of a resend generated it first.
			list, lerr := r.e.store.Artifacts().ListByRun(ctx, run.ID)
			for i := range list {
				if lerr == nil && list[i].Format == key {
					return &list[i], nil
				}
			}
		}
		return nil, err
	}
	return a, nil
}

// artifactDays reads retention_artifacts_days (30 by default).
func (e *Engine) artifactDays(ctx context.Context) int {
	return e.store.Settings().Int(ctx, store.SettingRetentionArtifactsDays, DefaultArtifactDays)
}
