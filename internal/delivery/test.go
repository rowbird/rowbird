package delivery

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/store"
)

// testTarget mirrors reports.TestTarget (the run stores it as JSON).
type testTarget struct {
	ChannelID *uuid.UUID `json:"channel_id,omitempty"`
	Email     string     `json:"email,omitempty"`
}

// TestDeliveries are the deliveries of a test run: the report's deliveries on the chosen channel,
// or an email to the caller through the system mailer with the report's file formats.
func (e *Engine) TestDeliveries(ctx context.Context, run *store.Run) ([]store.Delivery, error) {
	var t testTarget
	if err := json.Unmarshal(run.TestTarget, &t); err != nil {
		return nil, err
	}
	all, err := e.store.Deliveries().ListByReport(ctx, run.ReportID)
	if err != nil {
		return nil, err
	}
	if t.ChannelID != nil {
		var out []store.Delivery
		for _, d := range all {
			if d.ChannelID == *t.ChannelID {
				out = append(out, d)
			}
		}
		return out, nil
	}
	mailer, err := e.store.Channels().SystemMailer(ctx)
	if err != nil {
		return nil, err
	}
	var formats []string
	for _, d := range all {
		for _, f := range d.Formats {
			if !slices.Contains(formats, f) {
				formats = append(formats, f)
			}
		}
	}
	mode := plugin.ModeAttachment
	if len(formats) == 0 {
		mode = plugin.ModeInline
	}
	return []store.Delivery{{
		ReportID: run.ReportID, ChannelID: mailer.ID, Enabled: true, Mode: mode, Formats: formats, InlineRowLimit: 20,
		IncludeInlineWithFiles: true, LinkExpiresSeconds: 24 * 3600, Options: map[string]any{"to": t.Email},
	}}, nil
}
