package delivery

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/store"
)

// Preview renders what a delivery would send for a report, without sending anything, creating
// links or storing files. It uses the sample of run (the latest one with a result), or no rows when
// the report has not run yet.
func (e *Engine) Preview(ctx context.Context, rp *store.Report, run *store.Run, d store.Delivery, loc *time.Location) (plugin.Preview, error) {
	dest, env, _, err := e.channels.Env(ctx, d.ChannelID)
	if err != nil {
		return plugin.Preview{}, err
	}
	opts, err := dest.DeliverySchema().Validate(d.Options)
	if err != nil {
		return plugin.Preview{}, err
	}
	env.Options, env.Locale = opts, e.locale(ctx)
	caps := plugin.CapabilitiesOf(dest, env.Config)
	if loc == nil {
		loc = time.UTC
	}
	if run == nil {
		now := e.clock()
		run = &store.Run{ReportID: rp.ID, Status: store.RunSuccess, StartedAt: &now}
		run.CreatedAt = now
	}
	r := &run2{e: e, rp: rp, run: run, locale: env.Locale, loc: loc}
	msg := r.base(d)
	cols, rows := sample(run)
	if caps.InlineTarget != "" && (d.Mode == plugin.ModeInline || d.IncludeInlineWithFiles) && len(cols) > 0 {
		maxChars := 0
		if caps.MaxTextChars > 0 {
			maxChars = max(caps.MaxTextChars-textReserve, 200)
		}
		msg.Inline, err = r.inline(ctx, caps.InlineTarget, cols, rows, d.InlineRowLimit, maxChars, msg.Run.URL)
		if err != nil {
			return plugin.Preview{}, err
		}
	}
	if n, _ := opts["include_rows"].(int64); n > 0 {
		msg.Columns = cols
		msg.Rows = rows[:min(len(rows), int(n))]
	}
	if d.Mode != plugin.ModeInline {
		for _, f := range dedupe(d.Formats) {
			ext := f
			if p, ok := plugin.Get(plugin.KindFormatter, f); ok {
				if c, ok := p.Capabilities().(plugin.FormatterCapabilities); ok {
					ext = c.Extension
				}
			}
			name := FileName(rp, run, loc, ext)
			if d.Mode == plugin.ModeAttachment && caps.SupportsAttachments {
				msg.Attachments = append(msg.Attachments, plugin.Attachment{
					Name: name, Format: f, Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil },
				})
				continue
			}
			url := "#"
			if e.baseURL != "" {
				url = e.baseURL + "/r/rbl_…"
			}
			msg.Links = append(msg.Links, plugin.Link{Name: name, Format: f, URL: url, ExpiresAt: e.clock().Add(time.Duration(d.LinkExpiresSeconds) * time.Second)})
		}
	}
	return dest.Preview(ctx, env, msg)
}

// run2 is the part of a run's context a preview needs.
type run2 struct {
	e      *Engine
	rp     *store.Report
	run    *store.Run
	locale string
	loc    *time.Location
}

func (r *run2) base(d store.Delivery) plugin.Message {
	var reportURL, runURL string
	if r.e.baseURL != "" {
		reportURL = r.e.baseURL + "/reports/" + r.rp.ID.String()
		runURL = r.e.baseURL + "/runs/" + r.run.ID.String()
	}
	msg := plugin.Message{
		DeliveryID: d.ID.String(), RunID: r.run.ID.String(),
		Report: plugin.MessageReport{ID: r.rp.ID.String(), Title: r.rp.Title, Slug: r.rp.Slug, URL: reportURL},
		Run:    plugin.MessageRun{ID: r.run.ID.String(), URL: runURL, Status: r.run.Status, Truncated: r.run.Truncated},
		Status: plugin.StatusUp, Locale: r.locale, Location: r.loc,
	}
	if r.run.RowCount != nil {
		msg.Run.Rows = *r.run.RowCount
	}
	if r.run.DurationMS != nil {
		msg.Run.DurationMS = *r.run.DurationMS
	}
	if r.run.StartedAt != nil {
		msg.Run.StartedAt = *r.run.StartedAt
	}
	return msg
}

func (r *run2) inline(ctx context.Context, target string, cols []plugin.Column, rows [][]any, maxRows, maxChars int, link string) (string, error) {
	p, ok := plugin.Get(plugin.KindFormatter, inlineFormatters[target])
	f, isFormatter := p.(plugin.Formatter)
	if !ok || !isFormatter {
		return "", nil
	}
	in := plugin.FormatInput{
		Columns: cols, Rows: format.SliceRows(rows), RowCount: int64(len(rows)), Truncated: r.run.Truncated,
		Locale: r.locale, Location: r.loc, Title: r.rp.Title, MaxRows: maxRows, MaxChars: maxChars, LinkURL: link, Options: map[string]any{},
	}
	if r.run.RowCount != nil {
		in.RowCount = *r.run.RowCount
	}
	var buf bytes.Buffer
	_, err := f.Format(ctx, in, &buf)
	return buf.String(), err
}

// sample decodes a run's stored sample back into typed values.
func sample(run *store.Run) ([]plugin.Column, [][]any) {
	var s struct {
		Columns []plugin.Column `json:"columns"`
		Rows    [][]any         `json:"rows"`
	}
	if len(run.ResultSample) == 0 || json.Unmarshal(run.ResultSample, &s) != nil {
		return nil, nil
	}
	for _, row := range s.Rows {
		for i, v := range row {
			if i < len(s.Columns) {
				row[i] = typed(s.Columns[i].Type, v)
			}
		}
	}
	return s.Columns, s.Rows
}

func typed(t plugin.ValueType, v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case float64:
		switch t {
		case plugin.TypeInt:
			return int64(x)
		case plugin.TypeDecimal:
			return plugin.Decimal(strconv.FormatFloat(x, 'f', -1, 64))
		}
		return x
	case string:
		switch t {
		case plugin.TypeInt, plugin.TypeDecimal:
			return plugin.Decimal(x)
		case plugin.TypeDate:
			return plugin.Date(x)
		case plugin.TypeTime:
			return plugin.TimeOfDay(x)
		case plugin.TypeJSON:
			return plugin.JSON(x)
		case plugin.TypeDateTime:
			if ts, err := time.Parse(time.RFC3339Nano, x); err == nil {
				return ts
			}
		case plugin.TypeBinary:
			if b, err := base64.StdEncoding.DecodeString(x); err == nil {
				return b
			}
		}
		return x
	}
	return v
}
