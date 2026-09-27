// Package destinationtest is the conformance suite every destination must pass: metadata and
// translations, valid schemas, sending, previewing without sending, testing the channel, failures
// mapped to stable codes (retryable or not), secrets kept out of errors, and cancellation.
package destinationtest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/secretconfig"
)

// Harness describes how to exercise one destination against a fake service.
type Harness struct {
	Destination plugin.Destination
	// Config and Options are valid values pointing at the fake service.
	Config  map[string]any
	Options map[string]any
	// Sent returns how many deliveries the fake received so far.
	Sent func() int
	// Fail makes the fake answer with status, echoing the channel's secrets in its body; 0 resets.
	// Nil skips the failure checks (destinations that do not speak HTTP).
	Fail func(status int)
	// Inline is a sample of what the delivery engine would pass for the destination's target.
	Inline string
}

// Message returns a sample message: a successful run with one attachment and one link.
func Message(locale string) plugin.Message {
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	return plugin.Message{
		DeliveryID: "01900000-0000-7000-8000-00000000d001", RunID: "01900000-0000-7000-8000-00000000a001",
		Report: plugin.MessageReport{ID: "r1", Title: `Vendas por região <Q3> & "total"`, Slug: "vendas-por-regiao", URL: "https://rb.example.com/reports/r1"},
		Run: plugin.MessageRun{
			ID: "01900000-0000-7000-8000-00000000a001", URL: "https://rb.example.com/runs/a001", Status: "success",
			Rows: 1234, DurationMS: 1500, StartedAt: started,
		},
		Status: plugin.StatusUp, Locale: locale, Location: loc,
		Attachments: []plugin.Attachment{{
			Name: "vendas-por-regiao-2026-09-25.csv", Format: "csv", ContentType: "text/csv; charset=utf-8", Size: 22,
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("regiao;total\r\nSul;10\r\n")), nil
			},
		}},
		Links:   []plugin.Link{{Name: "vendas.xlsx", Format: "xlsx", URL: "https://rb.example.com/r/rbl_abc", ExpiresAt: started.Add(7 * 24 * time.Hour)}},
		Columns: []plugin.Column{{Name: "regiao", Type: plugin.TypeText}, {Name: "total", Type: plugin.TypeInt}},
		Rows:    [][]any{{"Sul", int64(10)}},
	}
}

// Env builds the environment with validated config and options.
func (h Harness) Env(t *testing.T) plugin.DestinationEnv {
	t.Helper()
	d := h.Destination
	cfg, err := d.ConfigSchema().Validate(h.Config)
	if err != nil {
		t.Fatalf("config does not validate: %v", err)
	}
	opts, err := d.DeliverySchema().Validate(h.Options)
	if err != nil {
		t.Fatalf("options do not validate: %v", err)
	}
	dial := netx.NewDialer(netx.PolicyOpen).DialContext
	return plugin.DestinationEnv{Config: cfg, Options: opts, HTTP: netx.HTTPClient(dial, 10*time.Second), Dial: dial, Locale: "en"}
}

// Run executes the suite.
func Run(t *testing.T, h Harness) {
	d := h.Destination
	caps, _ := d.Capabilities().(plugin.DestinationCapabilities)
	msg := func(locale string) plugin.Message {
		m := Message(locale)
		m.Inline = h.Inline
		if !caps.SupportsAttachments {
			m.Attachments = nil
		}
		return m
	}

	t.Run("metadata", func(t *testing.T) {
		m := d.Meta()
		if m.ID == "" || m.Version == "" {
			t.Fatal("metadata missing")
		}
		if len(caps.Modes) == 0 {
			t.Error("capabilities declare no delivery mode")
		}
		switch caps.InlineTarget {
		case "", plugin.InlineHTML, plugin.InlineMarkdown, plugin.InlineText:
		default:
			t.Errorf("unknown inline target %q", caps.InlineTarget)
		}
		if caps.AlwaysNotify && !caps.SupportsStatus {
			t.Error("always_notify needs supports_status")
		}
		keys := []string{m.Name, m.Description}
		for _, s := range []*plugin.Schema{d.ConfigSchema(), d.DeliverySchema()} {
			for _, f := range s.Fields {
				keys = append(keys, f.Label)
				if f.Help != "" {
					keys = append(keys, f.Help)
				}
				for _, v := range f.Enum {
					keys = append(keys, strings.TrimSuffix(f.Label, ".label")+"."+v)
				}
			}
		}
		for _, locale := range []string{"en", "pt-BR"} {
			for _, k := range keys {
				if d.Messages()[locale][k] == "" {
					t.Errorf("%s has no %s translation for %s", m.ID, locale, k)
				}
			}
		}
	})

	t.Run("preview sends nothing", func(t *testing.T) {
		before := h.Sent()
		for _, locale := range []string{"en", "pt-BR"} {
			p, err := d.Preview(context.Background(), h.Env(t), msg(locale))
			if err != nil {
				t.Fatal(err)
			}
			if p.Body == "" || (p.BodyType != "html" && p.BodyType != "text" && p.BodyType != "json") {
				t.Errorf("preview %+v", p)
			}
		}
		if h.Sent() != before {
			t.Error("preview sent something")
		}
	})

	t.Run("send", func(t *testing.T) {
		before := h.Sent()
		if _, err := d.Send(context.Background(), h.Env(t), msg("pt-BR")); err != nil {
			t.Fatal(err)
		}
		if h.Sent() == before {
			t.Error("nothing was sent")
		}
	})

	t.Run("alert", func(t *testing.T) {
		m := Message("pt-BR")
		m.Attachments, m.Links, m.Inline = nil, nil, ""
		m.Alert = &plugin.MessageAlert{
			Title: `Canal "ops" <falhando> & parado`, Text: "delivery.unreachable: connection refused",
			Severity: plugin.AlertError, URL: "https://rb.example.com/channels/c1",
		}
		before := h.Sent()
		_, err := d.Send(context.Background(), h.Env(t), m)
		if !caps.SupportsAlerts {
			var de *plugin.DeliveryError
			if !errors.As(err, &de) || de.Code != plugin.ErrCodeDeliveryRejected || h.Sent() != before {
				t.Errorf("a destination without alerts must refuse them: %v", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Sent() == before {
			t.Error("the alert was not sent")
		}
		m.Alert.Recovered, m.Alert.Severity = true, plugin.AlertInfo
		if _, err := d.Send(context.Background(), h.Env(t), m); err != nil {
			t.Fatalf("recovery: %v", err)
		}
	})

	t.Run("test", func(t *testing.T) {
		if err := d.Test(context.Background(), h.Env(t)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := d.Send(ctx, h.Env(t), msg("en")); err == nil {
			t.Error("send with a cancelled context succeeded")
		}
	})

	if h.Fail == nil {
		return
	}
	t.Run("failures", func(t *testing.T) {
		defer h.Fail(0)
		env := h.Env(t)
		for status, want := range map[int]struct {
			code  string
			retry bool
		}{
			401: {plugin.ErrCodeDeliveryAuth, false},
			429: {plugin.ErrCodeDeliveryRateLimited, true},
			503: {plugin.ErrCodeDeliveryFailed, true},
			400: {plugin.ErrCodeDeliveryRejected, false},
		} {
			h.Fail(status)
			_, err := d.Send(context.Background(), env, msg("en"))
			var de *plugin.DeliveryError
			if !errors.As(err, &de) || de.Code != want.code || de.Retry != want.retry {
				t.Errorf("HTTP %d: %v, want %s retry=%v", status, err, want.code, want.retry)
				continue
			}
			// The engine scrubs errors with the channel's secrets before storing them.
			scrubbed := secretconfig.Scrub(err.Error(), d.ConfigSchema(), env.Config)
			for _, k := range d.ConfigSchema().SecretKeys() {
				if v, _ := env.Config[k].(string); len(v) >= 3 && strings.Contains(scrubbed, v) {
					t.Errorf("HTTP %d: secret %s survives scrubbing: %s", status, k, scrubbed)
				}
			}
		}
	})
}

// ReadAll reads an attachment, for fakes and tests.
func ReadAll(a plugin.Attachment) []byte {
	r, err := a.Open()
	if err != nil {
		return nil
	}
	defer func() { _ = r.Close() }()
	var b bytes.Buffer
	_, _ = io.Copy(&b, r)
	return b.Bytes()
}
