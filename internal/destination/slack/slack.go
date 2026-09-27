// Package slack delivers runs to Slack, either with a bot token (messages and files, through the
// external upload flow) or with an incoming webhook (messages only) (docs/spec/04-plugins.md).
package slack

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/rowbird/rowbird/internal/destination"
	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/msgtemplate"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the plugin id.
const ID = "slack"

// Limits.
const (
	MaxText       = 40000
	MaxAttachment = 100 << 20
)

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MergeMessages(destination.Messages(), plugin.MustLoadMessages(locales))

func init() {
	plugin.Register(plugin.KindDestination, ID, func() plugin.Plugin { return dest{} })
}

type dest struct{}

func (dest) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.slack.name", Description: "plugin.slack.description", Icon: "slack", Version: "1.0.0"}
}

func (dest) ConfigSchema() *plugin.Schema {
	bot, hook := &plugin.ShowIf{Field: "auth", In: []any{"bot"}}, &plugin.ShowIf{Field: "auth", In: []any{"webhook"}}
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "auth", Type: plugin.TypeString, Default: "bot", Enum: []string{"bot", "webhook"}, Label: "plugin.slack.auth.label", Help: "plugin.slack.auth.help"},
		{Key: "bot_token", Type: plugin.TypeString, Required: true, Secret: true, MaxLength: 300, ShowIf: bot, Label: "plugin.slack.bot_token.label", Help: "plugin.slack.bot_token.help"},
		{Key: "webhook_url", Type: plugin.TypeString, Required: true, Secret: true, Format: "url", MaxLength: 500, ShowIf: hook, Label: "plugin.slack.webhook_url.label"},
		{Key: "api_base", Type: plugin.TypeString, Default: "https://slack.com/api", Format: "url", Group: "advanced", ShowIf: bot, Label: "plugin.slack.api_base.label"},
	}}
}

func (dest) DeliverySchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "channel", Type: plugin.TypeString, MaxLength: 100, Label: "plugin.slack.channel.label", Help: "plugin.slack.channel.help"},
		{Key: "message", Type: plugin.TypeString, Multiline: true, MaxLength: 3000, Label: "plugin.slack.message.label", Help: "plugin.template.help"},
	}}
}

func (dest) Capabilities() any { return caps(true) }

// CapabilitiesFor implements plugin.ConfiguredCapabilities: an incoming webhook cannot attach
// files.
func (dest) CapabilitiesFor(config map[string]any) plugin.DestinationCapabilities {
	return caps(format.String(config, "auth", "bot") == "bot")
}

func caps(bot bool) plugin.DestinationCapabilities {
	c := plugin.DestinationCapabilities{SupportsAlerts: true, MaxTextChars: MaxText, InlineTarget: plugin.InlineMarkdown, Modes: []string{plugin.ModeInline, plugin.ModeLink}}
	if bot {
		c.SupportsAttachments, c.MaxAttachmentBytes = true, MaxAttachment
		c.Modes = []string{plugin.ModeInline, plugin.ModeAttachment, plugin.ModeLink}
	}
	return c
}

func (dest) Messages() plugin.Messages { return messages }

func (dest) text(env plugin.DestinationEnv, msg plugin.Message) (string, error) {
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "|", "¦")
	if a := msg.Alert; a != nil {
		out := "*" + esc.Replace(a.Title) + "*\n\n" + esc.Replace(a.Text)
		if a.URL != "" {
			out += "\n\n<" + a.URL + "|" + esc.Replace(destination.AlertView(msg.Locale)) + ">"
		}
		text, _ := destination.FitText(out, MaxText)
		return text, nil
	}
	head, err := destination.Render(messages, env.Options, "message", "plugin.slack.message.default", msg, msgtemplate.Slack)
	if err != nil {
		return "", err
	}
	links := destination.LinkLines(msg, func(name, u string) string { return "<" + u + "|" + esc.Replace(name) + ">" })
	var view string
	if msg.Run.URL != "" {
		view = "<" + msg.Run.URL + "|" + destination.T(msg.Locale, "destination.view", nil) + ">"
	}
	out, _ := destination.Compose(MaxText, head, msg.Inline, links, view)
	return out, nil
}

func (d dest) Send(ctx context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.SendResult, error) {
	text, err := d.text(env, msg)
	if err != nil {
		return plugin.SendResult{}, err
	}
	if format.String(env.Config, "auth", "bot") == "webhook" {
		return plugin.SendResult{}, d.webhook(ctx, env, text)
	}
	channel := format.String(env.Options, "channel", "")
	if channel == "" {
		return plugin.SendResult{}, destination.Err(plugin.ErrCodeDeliveryRejected, false, errors.New("slack: the delivery has no channel"))
	}
	var posted struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	if err := d.api(ctx, env, "chat.postMessage", "application/json", jsonBody(map[string]any{
		"channel": channel, "text": text, "mrkdwn": true, "unfurl_links": false, "unfurl_media": false,
	}), &posted); err != nil {
		return plugin.SendResult{}, err
	}
	meta := map[string]any{"ts": posted.TS, "channel": posted.Channel}
	if len(msg.Attachments) == 0 {
		return plugin.SendResult{Meta: meta}, nil
	}
	var files []map[string]string
	for _, a := range msg.Attachments {
		id, err := d.upload(ctx, env, a)
		if err != nil {
			return plugin.SendResult{Meta: meta}, err
		}
		files = append(files, map[string]string{"id": id, "title": a.Name})
	}
	err = d.api(ctx, env, "files.completeUploadExternal", "application/json", jsonBody(map[string]any{
		"files": files, "channel_id": posted.Channel, "thread_ts": posted.TS,
	}), nil)
	return plugin.SendResult{Meta: meta}, err
}

// upload runs the first two steps of Slack's external upload: get an upload URL, send the bytes.
func (d dest) upload(ctx context.Context, env plugin.DestinationEnv, a plugin.Attachment) (string, error) {
	form := url.Values{"filename": {a.Name}, "length": {strconv.FormatInt(a.Size, 10)}}
	var target struct {
		UploadURL string `json:"upload_url"`
		FileID    string `json:"file_id"`
	}
	if err := d.api(ctx, env, "files.getUploadURLExternal", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), &target); err != nil {
		return "", err
	}
	r, err := a.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.UploadURL, r)
	if err != nil {
		return "", destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	req.ContentLength = a.Size
	req.Header.Set("Content-Type", "application/octet-stream")
	if _, _, err := destination.Do(env.HTTP, req); err != nil {
		return "", err
	}
	return target.FileID, nil
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

// api calls a Web API method. Slack answers 200 with ok=false for most failures.
func (d dest) api(ctx context.Context, env plugin.DestinationEnv, method, contentType string, body io.Reader, out any) error {
	base := strings.TrimRight(format.String(env.Config, "api_base", "https://slack.com/api"), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/"+method, body)
	if err != nil {
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	token, _ := env.Config["bot_token"].(string)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType+"; charset=utf-8")
	raw, _, err := destination.Do(env.HTTP, req)
	if err != nil {
		return err
	}
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return destination.Err(plugin.ErrCodeDeliveryFailed, true, fmt.Errorf("slack: %s: unexpected answer", method))
	}
	if !res.OK {
		return apiError(method, res.Error)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func apiError(method, code string) error {
	err := fmt.Errorf("slack: %s: %s", method, code)
	switch code {
	case "invalid_auth", "not_authed", "token_revoked", "token_expired", "account_inactive", "missing_scope":
		return destination.Err(plugin.ErrCodeDeliveryAuth, false, err)
	case "ratelimited":
		return destination.Err(plugin.ErrCodeDeliveryRateLimited, true, err)
	case "internal_error", "fatal_error", "service_unavailable":
		return destination.Err(plugin.ErrCodeDeliveryFailed, true, err)
	}
	return destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
}

func (d dest) webhook(ctx context.Context, env plugin.DestinationEnv, text string) error {
	u, _ := env.Config["webhook_url"].(string)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, jsonBody(map[string]any{"text": text, "mrkdwn": true}))
	if err != nil {
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	req.Header.Set("Content-Type", "application/json")
	_, _, err = destination.Do(env.HTTP, req)
	return err
}

// Test checks the token with auth.test, or posts a short message to the incoming webhook.
func (d dest) Test(ctx context.Context, env plugin.DestinationEnv) error {
	if format.String(env.Config, "auth", "bot") == "webhook" {
		return d.webhook(ctx, env, destination.T(env.Locale, "destination.testBody", nil))
	}
	return d.api(ctx, env, "auth.test", "application/x-www-form-urlencoded", strings.NewReader(""), nil)
}

func (d dest) Preview(_ context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.Preview, error) {
	text, err := d.text(env, msg)
	p := plugin.Preview{Body: text, BodyType: "text"}
	for _, a := range msg.Attachments {
		p.Attachments = append(p.Attachments, a.Name)
	}
	return p, err
}
