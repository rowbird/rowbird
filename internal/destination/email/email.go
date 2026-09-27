// Package email delivers runs by SMTP: an HTML message with the result as a table, a plain-text
// alternative, attachments up to a configurable size and links for the rest
// (docs/spec/04-plugins.md).
package email

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html"
	"net"
	"net/mail"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/rowbird/rowbird/internal/destination"
	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/msgtemplate"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the plugin id.
const ID = "email"

// Limits.
const (
	maxRecipients = 50
	sendTimeout   = 60 * time.Second
)

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MergeMessages(destination.Messages(), plugin.MustLoadMessages(locales))

func init() {
	plugin.Register(plugin.KindDestination, ID, func() plugin.Plugin { return dest{} })
}

type dest struct{}

func (dest) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.email.name", Description: "plugin.email.description", Icon: "mail", Version: "1.0.0"}
}

func (dest) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "host", Type: plugin.TypeString, Required: true, Format: "hostname", MaxLength: 255, Label: "plugin.email.host.label"},
		{Key: "port", Type: plugin.TypeInteger, Default: int64(587), Minimum: plugin.Float(1), Maximum: plugin.Float(65535), Label: "plugin.email.port.label"},
		{Key: "tls_mode", Type: plugin.TypeString, Default: "starttls", Enum: []string{"starttls", "tls", "none"}, Label: "plugin.email.tls_mode.label", Help: "plugin.email.tls_mode.help"},
		{Key: "username", Type: plugin.TypeString, MaxLength: 255, Label: "plugin.email.username.label"},
		{Key: "password", Type: plugin.TypeString, Secret: true, MaxLength: 500, Label: "plugin.email.password.label"},
		{Key: "from_address", Type: plugin.TypeString, Required: true, MaxLength: 255, Label: "plugin.email.from_address.label"},
		{Key: "from_name", Type: plugin.TypeString, Default: "Rowbird", MaxLength: 100, Label: "plugin.email.from_name.label"},
		{Key: "max_attachment_mb", Type: plugin.TypeInteger, Default: int64(20), Minimum: plugin.Float(1), Maximum: plugin.Float(200), Group: "advanced", Label: "plugin.email.max_attachment_mb.label", Help: "plugin.email.max_attachment_mb.help"},
	}}
}

func (dest) DeliverySchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "to", Type: plugin.TypeString, Required: true, MaxLength: 4000, Label: "plugin.email.to.label", Help: "plugin.email.recipients.help"},
		{Key: "cc", Type: plugin.TypeString, MaxLength: 4000, Label: "plugin.email.cc.label", Help: "plugin.email.recipients.help"},
		{Key: "bcc", Type: plugin.TypeString, MaxLength: 4000, Label: "plugin.email.bcc.label", Help: "plugin.email.recipients.help"},
		{Key: "subject", Type: plugin.TypeString, MaxLength: 500, Label: "plugin.email.subject.label", Help: "plugin.template.help"},
		{Key: "intro", Type: plugin.TypeString, Multiline: true, MaxLength: 4000, Label: "plugin.email.intro.label", Help: "plugin.template.help"},
	}}
}

func (d dest) Capabilities() any { return d.CapabilitiesFor(nil) }

// CapabilitiesFor implements plugin.ConfiguredCapabilities: the attachment limit is the channel's.
func (dest) CapabilitiesFor(config map[string]any) plugin.DestinationCapabilities {
	mb, ok := config["max_attachment_mb"].(int64)
	if !ok || mb <= 0 {
		mb = 20
	}
	return plugin.DestinationCapabilities{
		SupportsAttachments: true, MaxAttachmentBytes: mb << 20, InlineTarget: plugin.InlineHTML,
		SupportsAlerts: true, Modes: []string{plugin.ModeInline, plugin.ModeAttachment, plugin.ModeLink},
	}
}

func (dest) Messages() plugin.Messages { return messages }

// Recipients splits a list of addresses separated by commas or semicolons, and validates each
// one. It returns the addresses and the first invalid entry, if any.
func Recipients(s string) ([]string, string) {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		a, err := mail.ParseAddress(f)
		if err != nil {
			return nil, f
		}
		out = append(out, a.Address)
	}
	return out, ""
}

// ValidateOptions checks the recipient lists; the channel service calls it when a delivery is
// saved (the schema cannot express address lists).
func ValidateOptions(opts map[string]any) (string, bool) {
	for _, key := range []string{"to", "cc", "bcc"} {
		s, _ := opts[key].(string)
		list, bad := Recipients(s)
		if bad != "" || len(list) > maxRecipients {
			return key, false
		}
	}
	return "", true
}

type rendered struct {
	subject, html, text string
}

func (dest) render(env plugin.DestinationEnv, msg plugin.Message) (rendered, error) {
	var r rendered
	var err error
	if a := msg.Alert; a != nil {
		return renderAlert(msg.Locale, a), nil
	}
	if r.subject, err = destination.Render(messages, env.Options, "subject", "plugin.email.subject.default", msg, msgtemplate.None); err != nil {
		return r, err
	}
	r.subject = strings.Join(strings.Fields(r.subject), " ")
	intro, err := destination.Render(messages, env.Options, "intro", "plugin.email.intro.default", msg, msgtemplate.HTML)
	if err != nil {
		return r, err
	}
	introText, _ := destination.Render(messages, env.Options, "intro", "plugin.email.intro.default", msg, msgtemplate.None)
	title := msg.Report.Title
	if title == "" {
		title = destination.T(msg.Locale, "destination.testTitle", nil)
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html><body style="margin:0;padding:24px;background:#f6f7f9">`)
	b.WriteString(`<div style="max-width:760px;margin:0 auto;background:#ffffff;padding:24px;border:1px solid #e5e7eb;border-radius:8px;font-family:Arial,Helvetica,sans-serif;color:#1f2328">`)
	b.WriteString(`<h1 style="font-size:18px;margin:0 0 8px">` + html.EscapeString(title) + "</h1>\n")
	b.WriteString(`<p style="font-size:14px;color:#57606a;margin:0 0 16px">` + strings.ReplaceAll(intro, "\n", "<br>") + "</p>\n")
	b.WriteString(msg.Inline)
	if lines := destination.LinkLines(msg, func(name, u string) string {
		return `<a href="` + html.EscapeString(u) + `" style="color:#0969da">` + html.EscapeString(name) + "</a>"
	}); lines != "" {
		b.WriteString(`<p style="font-size:14px;margin:16px 0 0">` + strings.ReplaceAll(lines, "\n", "<br>") + "</p>\n")
	}
	if msg.Run.URL != "" {
		b.WriteString(`<p style="font-size:14px;margin:16px 0 0"><a href="` + html.EscapeString(msg.Run.URL) + `" style="color:#0969da">` +
			html.EscapeString(destination.T(msg.Locale, "destination.view", nil)) + "</a></p>\n")
	}
	b.WriteString(`<p style="font-size:12px;color:#8c959f;margin:24px 0 0">` + html.EscapeString(destination.T(msg.Locale, "destination.footer", nil)) + "</p>")
	b.WriteString("</div></body></html>\n")
	r.html = b.String()

	text := []string{title, introText}
	if msg.Inline != "" {
		text = append(text, destination.TFrom(messages, msg.Locale, "plugin.email.tableInHTML", nil))
	}
	text = append(text, destination.LinkLines(msg, func(name, u string) string { return name + ": " + u }))
	if msg.Run.URL != "" {
		text = append(text, destination.T(msg.Locale, "destination.view", nil)+": "+msg.Run.URL)
	}
	var parts []string
	for _, p := range text {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	r.text = strings.Join(parts, "\n\n") + "\n"
	return r, nil
}

// renderAlert builds a system alert email: the title as subject and heading, the text, and a link.
func renderAlert(locale string, a *plugin.MessageAlert) rendered {
	r := rendered{subject: strings.Join(strings.Fields(a.Title), " ")}
	var b strings.Builder
	b.WriteString(`<!doctype html><html><body style="margin:0;padding:24px;background:#f6f7f9">`)
	b.WriteString(`<div style="max-width:760px;margin:0 auto;background:#ffffff;padding:24px;border:1px solid #e5e7eb;border-radius:8px;font-family:Arial,Helvetica,sans-serif;color:#1f2328">`)
	b.WriteString(`<h1 style="font-size:18px;margin:0 0 8px">` + html.EscapeString(a.Title) + "</h1>\n")
	b.WriteString(`<p style="font-size:14px;margin:0 0 16px">` + strings.ReplaceAll(html.EscapeString(a.Text), "\n", "<br>") + "</p>\n")
	text := []string{a.Title, a.Text}
	if a.URL != "" {
		view := destination.AlertView(locale)
		b.WriteString(`<p style="font-size:14px;margin:16px 0 0"><a href="` + html.EscapeString(a.URL) + `" style="color:#0969da">` + html.EscapeString(view) + "</a></p>\n")
		text = append(text, view+": "+a.URL)
	}
	b.WriteString(`<p style="font-size:12px;color:#8c959f;margin:24px 0 0">` + html.EscapeString(destination.T(locale, "destination.footer", nil)) + "</p>")
	b.WriteString("</div></body></html>\n")
	r.html = b.String()
	r.text = strings.Join(text, "\n\n") + "\n"
	return r
}

func (d dest) Send(ctx context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.SendResult, error) {
	r, err := d.render(env, msg)
	if err != nil {
		return plugin.SendResult{}, err
	}
	m := gomail.NewMsg()
	if err := d.headers(m, env, r.subject); err != nil {
		return plugin.SendResult{}, err
	}
	for key, set := range map[string]func(...string) error{"to": m.To, "cc": m.Cc, "bcc": m.Bcc} {
		s, _ := env.Options[key].(string)
		list, bad := Recipients(s)
		if bad != "" {
			return plugin.SendResult{}, destination.Err(plugin.ErrCodeDeliveryRejected, false, fmt.Errorf("email: invalid %s address %q", key, bad))
		}
		if len(list) > 0 {
			if err := set(list...); err != nil {
				return plugin.SendResult{}, destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
			}
		}
	}
	m.SetBodyString(gomail.TypeTextPlain, r.text)
	m.AddAlternativeString(gomail.TypeTextHTML, r.html)
	for _, a := range msg.Attachments {
		rc, err := a.Open()
		if err != nil {
			return plugin.SendResult{}, err
		}
		err = m.AttachReader(a.Name, rc, gomail.WithFileContentType(gomail.ContentType(a.ContentType)))
		_ = rc.Close()
		if err != nil {
			return plugin.SendResult{}, err
		}
	}
	if err := d.send(ctx, env, m); err != nil {
		return plugin.SendResult{}, err
	}
	return plugin.SendResult{Meta: map[string]any{"message_id": m.GetMessageID()}}, nil
}

func (dest) headers(m *gomail.Msg, env plugin.DestinationEnv, subject string) error {
	from, _ := env.Config["from_address"].(string)
	if err := m.FromFormat(format.String(env.Config, "from_name", "Rowbird"), from); err != nil {
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	m.Subject(subject)
	m.SetMessageID()
	m.SetDate()
	m.SetGenHeader(gomail.HeaderXMailer, "Rowbird")
	return nil
}

func (dest) send(ctx context.Context, env plugin.DestinationEnv, m *gomail.Msg) error {
	host, _ := env.Config["host"].(string)
	port, _ := env.Config["port"].(int64)
	opts := []gomail.Option{
		gomail.WithPort(int(port)), gomail.WithTimeout(sendTimeout),
		gomail.WithDialContextFunc(func(ctx context.Context, network, addr string) (net.Conn, error) { return env.Dial(ctx, network, addr) }),
	}
	switch format.String(env.Config, "tls_mode", "starttls") {
	case "tls":
		opts = append(opts, gomail.WithSSL())
	case "none":
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	default:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	if user := format.String(env.Config, "username", ""); user != "" {
		pw, _ := env.Config["password"].(string)
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover), gomail.WithUsername(user), gomail.WithPassword(pw))
	}
	c, err := gomail.NewClient(host, opts...)
	if err != nil {
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	if err := c.DialAndSendWithContext(ctx, m); err != nil {
		return mapError(err)
	}
	return nil
}

// mapError turns SMTP failures into delivery codes: authentication, permanent rejections,
// temporary failures (retried) and network problems.
func mapError(err error) error {
	var se *gomail.SendError
	if errors.As(err, &se) {
		code := se.ErrorCode()
		switch {
		case code == 535 || code == 534 || code == 530:
			return destination.Err(plugin.ErrCodeDeliveryAuth, false, err)
		case code == 552:
			return destination.Err(plugin.ErrCodeDeliveryTooLarge, false, err)
		case se.IsTemp() || (code >= 400 && code < 500):
			return destination.Err(plugin.ErrCodeDeliveryFailed, true, err)
		case code >= 500:
			return destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
		}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "auth") && (strings.Contains(msg, "535") || strings.Contains(msg, "credentials") || strings.Contains(msg, "authentication")):
		return destination.Err(plugin.ErrCodeDeliveryAuth, false, err)
	case strings.Contains(msg, "tls") || strings.Contains(msg, "certificate"):
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	return destination.NetworkError(err)
}

// Test sends a short message: to the delivery's "to" when given (the "send test to me" flow sets
// it), otherwise to the channel's own address.
func (d dest) Test(ctx context.Context, env plugin.DestinationEnv) error {
	to, _ := env.Options["to"].(string)
	if to == "" {
		to, _ = env.Config["from_address"].(string)
	}
	list, bad := Recipients(to)
	if bad != "" || len(list) == 0 {
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, fmt.Errorf("email: invalid address %q", bad))
	}
	m := gomail.NewMsg()
	if err := d.headers(m, env, destination.T(env.Locale, "destination.testTitle", nil)); err != nil {
		return err
	}
	if err := m.To(list...); err != nil {
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	m.SetBodyString(gomail.TypeTextPlain, destination.T(env.Locale, "destination.testBody", nil)+"\n")
	return d.send(ctx, env, m)
}

func (d dest) Preview(_ context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.Preview, error) {
	r, err := d.render(env, msg)
	p := plugin.Preview{Subject: r.subject, Body: r.html, BodyType: "html"}
	for _, a := range msg.Attachments {
		p.Attachments = append(p.Attachments, a.Name)
	}
	for _, l := range msg.Links {
		p.Links = append(p.Links, l.Name)
	}
	return p, err
}

// ValidateDelivery implements plugin.DeliveryValidator.
func (dest) ValidateDelivery(opts map[string]any) (string, string, bool) {
	if field, ok := ValidateOptions(opts); !ok {
		return field, "validation.email", false
	}
	return "", "", true
}
