// Package webhook delivers runs as JSON to a URL, signed with HMAC-SHA256 so that the receiver
// can verify the sender and reject replays (docs/spec/07-security.md, "Outgoing webhooks").
//
// The signature header is X-Rowbird-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256 of
// "<t>.<body>">. Receivers should reject timestamps older than five minutes.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rowbird/rowbird/internal/destination"
	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the plugin id.
const ID = "webhook"

// Events sent in X-Rowbird-Event.
const (
	EventRun   = "run.completed"
	EventTest  = "test"
	EventAlert = "alert"
)

// MaxRows bounds the rows a payload may include.
const MaxRows = 1000

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MergeMessages(destination.Messages(), plugin.MustLoadMessages(locales))

// Now is the clock used for signatures; tests replace it.
var Now = time.Now

func init() {
	plugin.Register(plugin.KindDestination, ID, func() plugin.Plugin { return dest{} })
}

type dest struct{}

func (dest) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.webhook.name", Description: "plugin.webhook.description", Icon: "webhook", Version: "1.0.0"}
}

func (dest) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "url", Type: plugin.TypeString, Required: true, Format: "url", MaxLength: 2000, Label: "plugin.webhook.url.label"},
		{Key: "method", Type: plugin.TypeString, Default: "POST", Enum: []string{"POST", "PUT"}, Label: "plugin.webhook.method.label"},
		{Key: "headers", Type: plugin.TypeString, Multiline: true, MaxLength: 4000, Label: "plugin.webhook.headers.label", Help: "plugin.webhook.headers.help"},
		{Key: "secret_headers", Type: plugin.TypeString, Multiline: true, Secret: true, MaxLength: 4000, Label: "plugin.webhook.secret_headers.label", Help: "plugin.webhook.headers.help"},
		{Key: "hmac_secret", Type: plugin.TypeString, Secret: true, MaxLength: 500, Label: "plugin.webhook.hmac_secret.label", Help: "plugin.webhook.hmac_secret.help"},
	}}
}

func (dest) DeliverySchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "include_rows", Type: plugin.TypeInteger, Default: int64(0), Minimum: plugin.Float(0), Maximum: plugin.Float(MaxRows), Label: "plugin.webhook.include_rows.label", Help: "plugin.webhook.include_rows.help"},
		{Key: "include_links", Type: plugin.TypeBoolean, Default: true, Label: "plugin.webhook.include_links.label"},
	}}
}

func (dest) Capabilities() any {
	return plugin.DestinationCapabilities{SupportsAlerts: true, Modes: []string{plugin.ModeInline, plugin.ModeLink}}
}

func (dest) Messages() plugin.Messages { return messages }

// Payload is the JSON body.
type Payload struct {
	Event      string               `json:"event"`
	DeliveryID string               `json:"delivery_id,omitempty"`
	Status     string               `json:"status,omitempty"`
	Report     plugin.MessageReport `json:"report"`
	Run        plugin.MessageRun    `json:"run"`
	Columns    []column             `json:"columns,omitempty"`
	Rows       []map[string]any     `json:"rows,omitempty"`
	Links      []plugin.Link        `json:"links,omitempty"`
	Test       bool                 `json:"test,omitempty"`
}

type column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func payload(env plugin.DestinationEnv, msg plugin.Message) Payload {
	p := Payload{Event: EventRun, DeliveryID: msg.DeliveryID, Status: msg.Status, Report: msg.Report, Run: msg.Run}
	if n, _ := env.Options["include_rows"].(int64); n > 0 && len(msg.Columns) > 0 {
		for _, c := range msg.Columns {
			p.Columns = append(p.Columns, column{Name: c.Name, Type: string(c.Type)})
		}
		for i, row := range msg.Rows {
			if int64(i) >= n {
				break
			}
			obj := make(map[string]any, len(row))
			for j, v := range row {
				if j < len(msg.Columns) {
					obj[msg.Columns[j].Name] = plugin.JSONValue(v)
				}
			}
			p.Rows = append(p.Rows, obj)
		}
	}
	if b, ok := env.Options["include_links"].(bool); !ok || b {
		p.Links = msg.Links
	}
	return p
}

// AlertPayload is the body of a system alert.
type AlertPayload struct {
	Event string               `json:"event"`
	Alert *plugin.MessageAlert `json:"alert"`
}

func encodeBody(env plugin.DestinationEnv, msg plugin.Message) (string, []byte, error) {
	if msg.Alert != nil {
		b, err := json.Marshal(AlertPayload{Event: EventAlert, Alert: msg.Alert})
		return EventAlert, b, err
	}
	event := EventRun
	if msg.Test {
		event = EventTest
	}
	b, err := json.Marshal(payload(env, msg))
	return event, b, err
}

func (d dest) Send(ctx context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.SendResult, error) {
	event, body, err := encodeBody(env, msg)
	if err != nil {
		return plugin.SendResult{}, err
	}
	status, err := d.post(ctx, env, event, msg.DeliveryID, body)
	return plugin.SendResult{Meta: map[string]any{"http_status": status}}, err
}

func (d dest) post(ctx context.Context, env plugin.DestinationEnv, event, deliveryID string, body []byte) (int, error) {
	url, _ := env.Config["url"].(string)
	method := format.String(env.Config, "method", http.MethodPost)
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	for _, key := range []string{"headers", "secret_headers"} {
		for name, value := range ParseHeaders(format.String(env.Config, key, "")) {
			req.Header.Set(name, value)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Rowbird")
	req.Header.Set("X-Rowbird-Event", event)
	if deliveryID != "" {
		req.Header.Set("X-Rowbird-Delivery", deliveryID)
	}
	if secret := format.String(env.Config, "hmac_secret", ""); secret != "" {
		req.Header.Set("X-Rowbird-Signature", Sign(secret, Now(), body))
	}
	_, status, err := destination.Do(env.HTTP, req)
	return status, err
}

func (dest) Test(ctx context.Context, env plugin.DestinationEnv) error {
	body, err := json.Marshal(Payload{Event: EventTest, Test: true, Report: plugin.MessageReport{Title: destination.T(env.Locale, "destination.testTitle", nil)}})
	if err != nil {
		return err
	}
	_, err = dest{}.post(ctx, env, EventTest, "", body)
	return err
}

func (dest) Preview(_ context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.Preview, error) {
	b, err := json.MarshalIndent(payload(env, msg), "", "  ")
	return plugin.Preview{Body: string(b), BodyType: "json"}, err
}

// Sign returns the X-Rowbird-Signature value for body at t.
func Sign(secret string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a signature header, rejecting timestamps older than maxAge. Receivers written in
// Go can use it; the docs show the same steps for other languages.
func Verify(secret, header string, body []byte, now time.Time, maxAge time.Duration) bool {
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || now.Sub(time.Unix(sec, 0)) > maxAge || time.Unix(sec, 0).Sub(now) > maxAge {
		return false
	}
	want := Sign(secret, time.Unix(sec, 0), body)
	return hmac.Equal([]byte(want), []byte("t="+ts+",v1="+sig))
}

// ParseHeaders reads "Name: value" lines; malformed lines and Rowbird's own headers are ignored.
func ParseHeaders(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		name, value, ok := strings.Cut(line, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || name == "" || strings.ContainsAny(name, " \t") || strings.HasPrefix(strings.ToLower(name), "x-rowbird-") ||
			strings.EqualFold(name, "Content-Type") || strings.EqualFold(name, "Host") || strings.EqualFold(name, "Content-Length") {
			continue
		}
		out[name] = value
	}
	return out
}
