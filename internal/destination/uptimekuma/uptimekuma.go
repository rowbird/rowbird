// Package uptimekuma reports every run to an Uptime Kuma push monitor: up when the run succeeded,
// down when it failed, and a configurable status when its condition skipped it
// (docs/spec/04-plugins.md).
package uptimekuma

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/rowbird/rowbird/internal/destination"
	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/msgtemplate"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the plugin id.
const ID = "uptime_kuma"

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MergeMessages(destination.Messages(), plugin.MustLoadMessages(locales))

func init() {
	plugin.Register(plugin.KindDestination, ID, func() plugin.Plugin { return dest{} })
}

type dest struct{}

func (dest) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.uptime_kuma.name", Description: "plugin.uptime_kuma.description", Icon: "uptimekuma", Version: "1.0.0"}
}

func (dest) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "push_url", Type: plugin.TypeString, Required: true, Secret: true, Format: "url", MaxLength: 2000, Label: "plugin.uptime_kuma.push_url.label", Help: "plugin.uptime_kuma.push_url.help"},
	}}
}

func (dest) DeliverySchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "skipped_status", Type: plugin.TypeString, Default: "up", Enum: []string{"up", "down"}, Label: "plugin.uptime_kuma.skipped_status.label"},
		{Key: "message", Type: plugin.TypeString, MaxLength: 1000, Label: "plugin.uptime_kuma.message.label", Help: "plugin.template.help"},
	}}
}

func (dest) Capabilities() any {
	return plugin.DestinationCapabilities{SupportsStatus: true, AlwaysNotify: true, SupportsAlerts: true, Modes: []string{plugin.ModeInline}}
}

func (dest) Messages() plugin.Messages { return messages }

func pushURL(env plugin.DestinationEnv, msg plugin.Message) (string, string, error) {
	status := "up"
	switch msg.Status {
	case plugin.StatusDown:
		status = "down"
	case plugin.StatusSkipped:
		status = format.String(env.Options, "skipped_status", "up")
	}
	var text string
	var err error
	if a := msg.Alert; a != nil {
		// An alert is down until the message that ends it.
		status, text = "up", a.Title
		if a.Severity == plugin.AlertError && !a.Recovered {
			status = "down"
		}
	} else if text, err = destination.Render(messages, env.Options, "message", "plugin.uptime_kuma.message.default", msg, msgtemplate.None); err != nil {
		return "", "", err
	}
	raw, _ := env.Config["push_url"].(string)
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	q := u.Query()
	q.Set("status", status)
	q.Set("msg", text)
	q.Set("ping", strconv.FormatInt(msg.Run.DurationMS, 10))
	u.RawQuery = q.Encode()
	return u.String(), status, nil
}

func (d dest) Send(ctx context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.SendResult, error) {
	u, status, err := pushURL(env, msg)
	if err != nil {
		return plugin.SendResult{}, err
	}
	if err := push(ctx, env, u); err != nil {
		return plugin.SendResult{}, err
	}
	return plugin.SendResult{Meta: map[string]any{"status": status}}, nil
}

func push(ctx context.Context, env plugin.DestinationEnv, u string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	body, _, err := destination.Do(env.HTTP, req)
	if err != nil {
		return err
	}
	var res struct {
		OK  bool   `json:"ok"`
		Msg string `json:"msg"`
	}
	if json.Unmarshal(body, &res) == nil && !res.OK {
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, &kumaError{res.Msg})
	}
	return nil
}

type kumaError struct{ msg string }

func (e *kumaError) Error() string { return "uptime kuma: " + e.msg }

func (d dest) Test(ctx context.Context, env plugin.DestinationEnv) error {
	msg := plugin.Message{Status: plugin.StatusUp, Locale: env.Locale, Test: true, Report: plugin.MessageReport{Title: destination.T(env.Locale, "destination.testTitle", nil)}}
	u, _, err := pushURL(env, msg)
	if err != nil {
		return err
	}
	return push(ctx, env, u)
}

func (dest) Preview(_ context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.Preview, error) {
	text, err := destination.Render(messages, env.Options, "message", "plugin.uptime_kuma.message.default", msg, msgtemplate.None)
	status := "up"
	if msg.Status == plugin.StatusDown {
		status = "down"
	}
	return plugin.Preview{Subject: status, Body: text, BodyType: "text"}, err
}
