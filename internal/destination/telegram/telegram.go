// Package telegram delivers runs to a Telegram chat through a bot: the message in HTML parse mode
// (everything escaped), with the result as an aligned table, and files with sendDocument
// (docs/spec/04-plugins.md).
package telegram

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/rowbird/rowbird/internal/destination"
	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/msgtemplate"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the plugin id.
const ID = "telegram"

// Limits of the Bot API.
const (
	MaxText       = 4096
	MaxAttachment = 50 << 20
)

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MergeMessages(destination.Messages(), plugin.MustLoadMessages(locales))

func init() {
	plugin.Register(plugin.KindDestination, ID, func() plugin.Plugin { return dest{} })
}

type dest struct{}

func (dest) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.telegram.name", Description: "plugin.telegram.description", Icon: "telegram", Version: "1.0.0"}
}

func (dest) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "bot_token", Type: plugin.TypeString, Required: true, Secret: true, MaxLength: 200, Label: "plugin.telegram.bot_token.label", Help: "plugin.telegram.bot_token.help"},
		{Key: "api_base", Type: plugin.TypeString, Default: "https://api.telegram.org", Format: "url", Group: "advanced", Label: "plugin.telegram.api_base.label", Help: "plugin.telegram.api_base.help"},
	}}
}

func (dest) DeliverySchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "chat_id", Type: plugin.TypeString, Required: true, MaxLength: 100, Label: "plugin.telegram.chat_id.label", Help: "plugin.telegram.chat_id.help"},
		{Key: "message", Type: plugin.TypeString, Multiline: true, MaxLength: 2000, Label: "plugin.telegram.message.label", Help: "plugin.template.help"},
		{Key: "silent", Type: plugin.TypeBoolean, Default: false, Label: "plugin.telegram.silent.label"},
	}}
}

func (dest) Capabilities() any {
	return plugin.DestinationCapabilities{
		SupportsAttachments: true, MaxAttachmentBytes: MaxAttachment, MaxTextChars: MaxText, InlineTarget: plugin.InlineText,
		SupportsAlerts: true, Modes: []string{plugin.ModeInline, plugin.ModeAttachment, plugin.ModeLink},
	}
}

func (dest) Messages() plugin.Messages { return messages }

func (dest) text(env plugin.DestinationEnv, msg plugin.Message) (string, error) {
	if a := msg.Alert; a != nil {
		out := "<b>" + html.EscapeString(a.Title) + "</b>\n\n" + html.EscapeString(a.Text)
		if a.URL != "" {
			out += fmt.Sprintf("\n\n<a href=\"%s\">%s</a>", html.EscapeString(a.URL), html.EscapeString(destination.AlertView(msg.Locale)))
		}
		text, _ := destination.FitText(out, MaxText)
		return text, nil
	}
	head, err := destination.Render(messages, env.Options, "message", "plugin.telegram.message.default", msg, msgtemplate.HTML)
	if err != nil {
		return "", err
	}
	links := destination.LinkLines(msg, func(name, url string) string {
		return fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(url), html.EscapeString(name))
	})
	var view string
	if msg.Run.URL != "" {
		view = fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(msg.Run.URL), html.EscapeString(destination.T(msg.Locale, "destination.view", nil)))
	}
	out, _ := destination.Compose(MaxText, head, msg.Inline, links, view)
	return out, nil
}

func (d dest) api(env plugin.DestinationEnv, method string) string {
	base := strings.TrimRight(format.String(env.Config, "api_base", "https://api.telegram.org"), "/")
	token, _ := env.Config["bot_token"].(string)
	return base + "/bot" + token + "/" + method
}

type response struct {
	OK          bool            `json:"ok"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func (d dest) call(ctx context.Context, env plugin.DestinationEnv, method, contentType string, body io.Reader) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.api(env, method), body)
	if err != nil {
		return nil, destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	req.Header.Set("Content-Type", contentType)
	raw, status, err := destination.Do(env.HTTP, req)
	var res response
	if json.Unmarshal(raw, &res) == nil && !res.OK && res.Description != "" {
		// Telegram explains failures in the body; keep its words, never the URL (it has the token).
		code := destination.StatusError(max(status, res.ErrorCode), res.Description)
		return nil, code
	}
	if err != nil {
		return nil, err
	}
	return res.Result, nil
}

func (d dest) Send(ctx context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.SendResult, error) {
	text, err := d.text(env, msg)
	if err != nil {
		return plugin.SendResult{}, err
	}
	chat, _ := env.Options["chat_id"].(string)
	silent, _ := env.Options["silent"].(bool)
	body, _ := json.Marshal(map[string]any{
		"chat_id": chat, "text": text, "parse_mode": "HTML", "disable_notification": silent,
		"link_preview_options": map[string]bool{"is_disabled": true},
	})
	result, err := d.call(ctx, env, "sendMessage", "application/json", bytes.NewReader(body))
	if err != nil {
		return plugin.SendResult{}, err
	}
	meta := map[string]any{}
	var sent struct {
		MessageID int64 `json:"message_id"`
	}
	if json.Unmarshal(result, &sent) == nil {
		meta["message_id"] = sent.MessageID
	}
	for _, a := range msg.Attachments {
		if err := d.document(ctx, env, chat, silent, a); err != nil {
			return plugin.SendResult{Meta: meta}, err
		}
	}
	return plugin.SendResult{Meta: meta}, nil
}

func (d dest) document(ctx context.Context, env plugin.DestinationEnv, chat string, silent bool, a plugin.Attachment) error {
	r, err := a.Open()
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", chat)
	if silent {
		_ = w.WriteField("disable_notification", "true")
	}
	part, err := w.CreateFormFile("document", a.Name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, r); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	_, err = d.call(ctx, env, "sendDocument", w.FormDataContentType(), &buf)
	return err
}

// Test checks the token with getMe (a channel has no chat; deliveries name it).
func (d dest) Test(ctx context.Context, env plugin.DestinationEnv) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.api(env, "getMe"), nil)
	if err != nil {
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	_, _, err = destination.Do(env.HTTP, req)
	return err
}

func (d dest) Preview(_ context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.Preview, error) {
	text, err := d.text(env, msg)
	p := plugin.Preview{Body: text, BodyType: "html"}
	for _, a := range msg.Attachments {
		p.Attachments = append(p.Attachments, a.Name)
	}
	return p, err
}
