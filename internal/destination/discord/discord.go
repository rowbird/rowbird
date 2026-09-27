// Package discord delivers runs to a Discord channel through a webhook: a message of up to 2000
// characters and files up to the server's upload limit (docs/spec/04-plugins.md).
package discord

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"

	"github.com/rowbird/rowbird/internal/destination"
	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/msgtemplate"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the plugin id.
const ID = "discord"

// MaxText is Discord's message limit.
const MaxText = 2000

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MergeMessages(destination.Messages(), plugin.MustLoadMessages(locales))

func init() {
	plugin.Register(plugin.KindDestination, ID, func() plugin.Plugin { return dest{} })
}

type dest struct{}

func (dest) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.discord.name", Description: "plugin.discord.description", Icon: "discord", Version: "1.0.0"}
}

func (dest) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "webhook_url", Type: plugin.TypeString, Required: true, Secret: true, Format: "url", MaxLength: 500, Label: "plugin.discord.webhook_url.label"},
		{Key: "max_attachment_mb", Type: plugin.TypeInteger, Default: int64(8), Minimum: plugin.Float(1), Maximum: plugin.Float(500), Group: "advanced", Label: "plugin.discord.max_attachment_mb.label", Help: "plugin.discord.max_attachment_mb.help"},
	}}
}

func (dest) DeliverySchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "message", Type: plugin.TypeString, Multiline: true, MaxLength: 1500, Label: "plugin.discord.message.label", Help: "plugin.template.help"},
		{Key: "username", Type: plugin.TypeString, MaxLength: 80, Label: "plugin.discord.username.label"},
	}}
}

func (d dest) Capabilities() any { return d.CapabilitiesFor(nil) }

// CapabilitiesFor implements plugin.ConfiguredCapabilities: the upload limit depends on the
// server's boost level, so it is configurable.
func (dest) CapabilitiesFor(config map[string]any) plugin.DestinationCapabilities {
	mb, ok := config["max_attachment_mb"].(int64)
	if !ok || mb <= 0 {
		mb = 8
	}
	return plugin.DestinationCapabilities{
		SupportsAttachments: true, MaxAttachmentBytes: mb << 20, MaxTextChars: MaxText, InlineTarget: plugin.InlineMarkdown,
		SupportsAlerts: true, Modes: []string{plugin.ModeInline, plugin.ModeAttachment, plugin.ModeLink},
	}
}

func (dest) Messages() plugin.Messages { return messages }

func (dest) text(env plugin.DestinationEnv, msg plugin.Message) (string, error) {
	if a := msg.Alert; a != nil {
		out := "**" + a.Title + "**\n\n" + a.Text
		if a.URL != "" {
			out += fmt.Sprintf("\n\n[%s](<%s>)", destination.AlertView(msg.Locale), a.URL)
		}
		text, _ := destination.FitText(out, MaxText)
		return text, nil
	}
	head, err := destination.Render(messages, env.Options, "message", "plugin.discord.message.default", msg, msgtemplate.None)
	if err != nil {
		return "", err
	}
	links := destination.LinkLines(msg, func(name, u string) string { return fmt.Sprintf("[%s](<%s>)", name, u) })
	var view string
	if msg.Run.URL != "" {
		view = fmt.Sprintf("[%s](<%s>)", destination.T(msg.Locale, "destination.view", nil), msg.Run.URL)
	}
	out, _ := destination.Compose(MaxText, head, msg.Inline, links, view)
	return out, nil
}

func (d dest) Send(ctx context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.SendResult, error) {
	text, err := d.text(env, msg)
	if err != nil {
		return plugin.SendResult{}, err
	}
	payload := map[string]any{"content": text, "allowed_mentions": map[string]any{"parse": []string{}}}
	if u := format.String(env.Options, "username", ""); u != "" {
		payload["username"] = u
	}
	body, contentType, err := encode(payload, msg.Attachments)
	if err != nil {
		return plugin.SendResult{}, err
	}
	raw, err := d.post(ctx, env, body, contentType)
	if err != nil {
		return plugin.SendResult{}, err
	}
	var sent struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &sent)
	return plugin.SendResult{Meta: map[string]any{"message_id": sent.ID}}, nil
}

// encode builds a JSON body, or a multipart one with payload_json and the files.
func encode(payload map[string]any, files []plugin.Attachment) (io.Reader, string, error) {
	pj, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	if len(files) == 0 {
		return bytes.NewReader(pj), "application/json", nil
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("payload_json", string(pj)); err != nil {
		return nil, "", err
	}
	for i, a := range files {
		part, err := w.CreateFormFile(fmt.Sprintf("files[%d]", i), a.Name)
		if err != nil {
			return nil, "", err
		}
		r, err := a.Open()
		if err != nil {
			return nil, "", err
		}
		_, err = io.Copy(part, r)
		_ = r.Close()
		if err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

func (d dest) post(ctx context.Context, env plugin.DestinationEnv, body io.Reader, contentType string) ([]byte, error) {
	raw, _ := env.Config["webhook_url"].(string)
	u, err := url.Parse(raw)
	if err != nil {
		return nil, destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	q := u.Query()
	q.Set("wait", "true")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), body)
	if err != nil {
		return nil, destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	req.Header.Set("Content-Type", contentType)
	out, _, err := destination.Do(env.HTTP, req)
	return out, err
}

// Test reads the webhook (Discord answers with its details), which checks the URL without posting.
func (d dest) Test(ctx context.Context, env plugin.DestinationEnv) error {
	raw, _ := env.Config["webhook_url"].(string)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	_, _, err = destination.Do(env.HTTP, req)
	return err
}

func (d dest) Preview(_ context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.Preview, error) {
	text, err := d.text(env, msg)
	p := plugin.Preview{Body: text, BodyType: "text"}
	for _, a := range msg.Attachments {
		p.Attachments = append(p.Attachments, a.Name)
	}
	return p, err
}
