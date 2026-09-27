package reports_test

import (
	"errors"
	"reflect"
	"testing"

	_ "github.com/rowbird/rowbird/internal/destination/email"
	_ "github.com/rowbird/rowbird/internal/destination/s3"
	_ "github.com/rowbird/rowbird/internal/destination/webhook"
	_ "github.com/rowbird/rowbird/internal/format/csv"
	_ "github.com/rowbird/rowbird/internal/format/inline"
	_ "github.com/rowbird/rowbird/internal/format/xlsx"
	"github.com/rowbird/rowbird/internal/reports"
	"github.com/rowbird/rowbird/internal/store"
)

func ptrInt(i int) *int { return &i }

func TestDeliveries(t *testing.T) {
	e := newEnv(t)
	svc := reports.NewService(e.Store, e.Queries, reports.Options{Now: e.Clock.Now, BaseURL: "https://rb.example.com"})
	v, err := svc.Create(e.Ctx, e.Principal, reports.Input{Title: "R", QueryID: e.query, Cron: "@daily"})
	if err != nil {
		t.Fatal(err)
	}
	rp := v.Report.ID
	mail := &store.Channel{Name: "mail", Type: "email", Config: map[string]any{"host": "smtp", "from_address": "a@example.com"}}
	hook := &store.Channel{Name: "hook", Type: "webhook", Config: map[string]any{"url": "https://h.example.com"}}
	bucket := &store.Channel{Name: "bucket", Type: "s3", Config: map[string]any{}}
	for _, c := range []*store.Channel{mail, hook, bucket} {
		if err := e.Store.Channels().Create(e.Ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	d, err := svc.CreateDelivery(e.Ctx, rp, reports.DeliveryInput{
		ChannelID: mail.ID, Mode: "attachment", Formats: []string{"xlsx", "csv", "xlsx"},
		Options: map[string]any{"to": "ana@example.com, bruno@example.com", "subject": "{{report.name}} {{run.date}}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.Formats, []string{"xlsx", "csv"}) || d.InlineRowLimit != 20 || d.LinkExpiresSeconds != 604800 || !d.Enabled || d.ChannelName != "mail" || d.Position != 0 {
		t.Fatalf("delivery %+v", d)
	}
	second, _ := svc.CreateDelivery(e.Ctx, rp, reports.DeliveryInput{ChannelID: hook.ID, Options: map[string]any{"include_rows": 10}})
	if second.Mode != "inline" || second.Position != 1 || second.Options["include_rows"] != float64(10) || len(second.Formats) != 0 {
		t.Fatalf("webhook delivery %+v", second)
	}

	cases := []struct {
		name string
		in   reports.DeliveryInput
		want map[string]string
	}{
		{"unknown channel", reports.DeliveryInput{ChannelID: e.query}, map[string]string{"channel_id": "validation.invalid_value"}},
		{"mode not supported", reports.DeliveryInput{ChannelID: hook.ID, Mode: "attachment", Formats: []string{"csv"}}, map[string]string{"mode": "validation.invalid_value"}},
		{"files without formats", reports.DeliveryInput{ChannelID: mail.ID, Mode: "attachment", Options: map[string]any{"to": "a@example.com"}}, map[string]string{"formats": "validation.required"}},
		{
			"not a file format",
			reports.DeliveryInput{ChannelID: mail.ID, Mode: "link", Formats: []string{"html_table", "nope"}, Options: map[string]any{"to": "a@example.com"}},
			map[string]string{"formats[0]": "validation.invalid_value", "formats[1]": "validation.invalid_value"},
		},
		{
			"bad options",
			reports.DeliveryInput{ChannelID: mail.ID, Mode: "inline", Options: map[string]any{"to": "not an address", "subject": "{{nope}}"}},
			map[string]string{"options.subject": "validation.template_variable", "options.to": "validation.email"},
		},
		{"missing required option", reports.DeliveryInput{ChannelID: mail.ID}, map[string]string{"options.to": "validation.required"}},
		{
			"limits",
			reports.DeliveryInput{ChannelID: hook.ID, InlineRowLimit: ptrInt(500), LinkExpiresSeconds: ptrInt(60)},
			map[string]string{"inline_row_limit": "validation.range", "link_expires_seconds": "validation.range"},
		},
	}
	for _, tc := range cases {
		_, err := svc.CreateDelivery(e.Ctx, rp, tc.in)
		if got := fields(t, err); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}

	// Links need a public address.
	noURL := reports.NewService(e.Store, e.Queries, reports.Options{Now: e.Clock.Now})
	_, err = noURL.CreateDelivery(e.Ctx, rp, reports.DeliveryInput{ChannelID: mail.ID, Mode: "link", Formats: []string{"csv"}, Options: map[string]any{"to": "a@example.com"}})
	if got := fields(t, err); got["mode"] != "validation.base_url_missing" {
		t.Errorf("link without base url: %v", got)
	}

	upd, err := svc.UpdateDelivery(e.Ctx, rp, d.ID, d.Version, reports.DeliveryInput{ChannelID: mail.ID, Mode: "inline", Enabled: new(bool), Options: map[string]any{"to": "c@example.com"}})
	if err != nil || upd.Mode != "inline" || upd.Enabled || len(upd.Formats) != 0 || upd.Options["to"] != "c@example.com" {
		t.Fatalf("update %+v %v", upd, err)
	}
	if _, err := svc.UpdateDelivery(e.Ctx, rp, d.ID, 1, reports.DeliveryInput{ChannelID: mail.ID, Options: map[string]any{"to": "c@example.com"}}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	other, _ := svc.Create(e.Ctx, e.Principal, reports.Input{Title: "Other", QueryID: e.query, Cron: "@daily"})
	if _, err := svc.UpdateDelivery(e.Ctx, other.Report.ID, d.ID, upd.Version, reports.DeliveryInput{ChannelID: mail.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delivery of another report: %v", err)
	}
	list, _ := svc.Deliveries(e.Ctx, rp)
	if len(list) != 2 {
		t.Fatalf("list %d", len(list))
	}
	if err := svc.DeleteDelivery(e.Ctx, rp, d.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.Deliveries(e.Ctx, rp); len(list) != 1 {
		t.Fatal("delivery not deleted")
	}
}
