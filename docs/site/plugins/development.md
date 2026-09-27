# Plugin development

Everything that varies in Rowbird is a plugin with a fixed contract: connectors, formats,
conditions, destinations and AI providers. Plugins are compiled in (there is no runtime loading),
so adding one means a Go package, one import line and passing the conformance suite of its kind.

This guide builds a destination. The other kinds follow the same steps with their own interface
in [`internal/plugin`](https://github.com/rowbird/rowbird/tree/main/internal/plugin).

## 1. The package

Destinations live in `internal/destination/<name>`. A minimal one that posts each run to an HTTP
endpoint looks like this (compare with the real
[Uptime Kuma destination](https://github.com/rowbird/rowbird/blob/main/internal/destination/uptimekuma/uptimekuma.go)):

```go
// Package ntfy sends a notification to an ntfy topic for every run.
package ntfy

import (
	"context"
	"embed"
	"net/http"
	"strings"

	"github.com/rowbird/rowbird/internal/destination"
	"github.com/rowbird/rowbird/internal/msgtemplate"
	"github.com/rowbird/rowbird/internal/plugin"
)

const ID = "ntfy"

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MergeMessages(destination.Messages(), plugin.MustLoadMessages(locales))

func init() {
	plugin.Register(plugin.KindDestination, ID, func() plugin.Plugin { return dest{} })
}

type dest struct{}

func (dest) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.ntfy.name", Description: "plugin.ntfy.description", Icon: "webhook", Version: "1.0.0"}
}

// ConfigSchema is the channel's configuration. Secret fields are encrypted, never returned by the
// API, never exported and removed from error messages.
func (dest) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "topic_url", Type: plugin.TypeString, Required: true, Secret: true, Format: "url", MaxLength: 2000, Label: "plugin.ntfy.topic_url.label"},
	}}
}

// DeliverySchema holds the options of each delivery to this channel.
func (dest) DeliverySchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "message", Type: plugin.TypeString, MaxLength: 1000, Label: "plugin.ntfy.message.label", Help: "plugin.template.help"},
	}}
}

func (dest) Capabilities() any {
	return plugin.DestinationCapabilities{MaxTextChars: 4096, Modes: []string{plugin.ModeInline, plugin.ModeLink}}
}

func (dest) Messages() plugin.Messages { return messages }

func (d dest) Send(ctx context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.SendResult, error) {
	text, err := destination.Render(messages, env.Options, "message", "plugin.ntfy.message.default", msg, msgtemplate.None)
	if err != nil {
		return plugin.SendResult{}, err
	}
	url, _ := env.Config["topic_url"].(string)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(text))
	if err != nil {
		return plugin.SendResult{}, destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	// env.HTTP applies the network policy; destination.Do maps statuses to stable error codes.
	if _, _, err := destination.Do(env.HTTP, req); err != nil {
		return plugin.SendResult{}, err
	}
	return plugin.SendResult{}, nil
}

func (d dest) Test(ctx context.Context, env plugin.DestinationEnv) error {
	msg := plugin.Message{Status: plugin.StatusUp, Locale: env.Locale, Test: true,
		Report: plugin.MessageReport{Title: destination.T(env.Locale, "destination.testTitle", nil)}}
	_, err := d.Send(ctx, env, msg)
	return err
}

func (dest) Preview(_ context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.Preview, error) {
	text, err := destination.Render(messages, env.Options, "message", "plugin.ntfy.message.default", msg, msgtemplate.None)
	return plugin.Preview{Body: text, BodyType: "text"}, err
}
```

Rules every destination follows:

- **Use `env.HTTP`** (or the dialer in `env`) for every network call, never `http.DefaultClient`:
  it enforces the network policy.
- **Return `destination.Err(code, retry, err)`** with a stable code (`delivery.auth_failed`,
  `delivery.rejected`, `delivery.rate_limited`, ...) and say whether a retry can help.
  `destination.Do` already maps HTTP statuses and network failures.
- **Never put a secret in an error.** The engine scrubs known secret values, but do not rely on it.
- **Respect `ctx`.** Cancellation must stop the send.
- **Escape for the destination.** Templates render through `msgtemplate` with the right escaper.

## 2. Translations

Labels are i18n keys. Put the English and Brazilian Portuguese texts in
`locales/en.json` and `locales/pt-BR.json` inside the package:

```json
{
  "plugin.ntfy.name": "ntfy",
  "plugin.ntfy.description": "Sends a notification to an ntfy topic for every run.",
  "plugin.ntfy.topic_url.label": "Topic URL",
  "plugin.ntfy.message.label": "Message",
  "plugin.ntfy.message.default": "{{report.name}}: {{run.status}}, {{run.rows}} rows"
}
```

`make i18n-check` fails when a key exists in one language and not the other.

## 3. Registration

Add a blank import to
[`internal/app/plugins.go`](https://github.com/rowbird/rowbird/blob/main/internal/app/plugins.go):

```go
_ "github.com/rowbird/rowbird/internal/destination/ntfy"
```

The plugin now appears in `GET /api/v1/plugins`, the UI renders its form from the schema, and
YAML imports validate against it. No frontend code is needed. If the plugin needs an icon, add it
to the UI's icon map; unknown icons show a plug.

## 4. Tests

Each kind has a conformance suite: `destinationtest`, `connectortest`, `formattest`,
`conditiontest` and `aitest`. Run it against a fake service:

```go
func TestConformance(t *testing.T) {
	var mu sync.Mutex
	sent, fail := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if fail != 0 {
			w.WriteHeader(fail)
			return
		}
		sent++
	}))
	defer srv.Close()
	destinationtest.Run(t, destinationtest.Harness{
		Destination: dest{},
		Config:      map[string]any{"topic_url": srv.URL + "/rowbird-s3cret-topic"},
		Sent:        func() int { mu.Lock(); defer mu.Unlock(); return sent },
		Fail:        func(status int) { mu.Lock(); fail = status; mu.Unlock() },
	})
}
```

The suite checks metadata and translations, valid schemas, sending and testing, previews that send
nothing, failures mapped to stable codes with the right retry decision, that no secret leaks into
errors, and cancellation. Add table-driven tests for your own behavior next to it, and an
integration test behind the `integration` build tag if a real service can run in a container.

## 5. Submit it

Open an issue with the "New destination" or "New connector" template first, so we can agree on the
configuration fields. Then follow [CONTRIBUTING.md](https://github.com/rowbird/rowbird/blob/main/CONTRIBUTING.md):
`make lint`, `make test`, docs for the new plugin, and a Conventional Commit such as
`feat(destination/ntfy): add ntfy notifications`.
