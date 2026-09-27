package runner_test

import (
	"context"
	"errors"
	"sync"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/destinationtest"
)

// fakeDest records what it receives. Its configuration steers it: max_kb bounds attachments,
// fail_times fails that many sends with a retryable error, fail_auth always fails for good,
// always_notify turns it into a status destination (like Uptime Kuma).
type fakeDest struct{}

type received struct {
	channel string
	msg     plugin.Message
	files   map[string]string
}

var (
	destMu   sync.Mutex
	inbox    []received
	failures = map[string]int{}
)

func takeInbox() []received {
	destMu.Lock()
	defer destMu.Unlock()
	out := inbox
	inbox = nil
	return out
}

func (fakeDest) Meta() plugin.Metadata {
	return plugin.Metadata{ID: "fakedest", Name: "fakedest", Version: "1"}
}

func (fakeDest) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "name", Type: plugin.TypeString, Required: true, Label: "l"},
		{Key: "max_kb", Type: plugin.TypeInteger, Default: int64(1024), Label: "l"},
		{Key: "fail_times", Type: plugin.TypeInteger, Default: int64(0), Label: "l"},
		{Key: "fail_auth", Type: plugin.TypeBoolean, Default: false, Label: "l"},
		{Key: "always_notify", Type: plugin.TypeBoolean, Default: false, Label: "l"},
		{Key: "token", Type: plugin.TypeString, Secret: true, Label: "l"},
	}}
}

func (fakeDest) DeliverySchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "include_rows", Type: plugin.TypeInteger, Default: int64(0), Label: "l"},
		{Key: "to", Type: plugin.TypeString, Label: "l"},
	}}
}
func (fakeDest) Capabilities() any         { return plugin.DestinationCapabilities{} }
func (fakeDest) Messages() plugin.Messages { return nil }
func (fakeDest) CapabilitiesFor(cfg map[string]any) plugin.DestinationCapabilities {
	kb, _ := cfg["max_kb"].(int64)
	always, _ := cfg["always_notify"].(bool)
	return plugin.DestinationCapabilities{
		SupportsAttachments: true, MaxAttachmentBytes: kb << 10, MaxTextChars: 4000, InlineTarget: plugin.InlineHTML,
		SupportsStatus: always, AlwaysNotify: always, Modes: []string{plugin.ModeInline, plugin.ModeAttachment, plugin.ModeLink},
	}
}

func (fakeDest) Send(_ context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.SendResult, error) {
	name, _ := env.Config["name"].(string)
	destMu.Lock()
	defer destMu.Unlock()
	if auth, _ := env.Config["fail_auth"].(bool); auth {
		return plugin.SendResult{}, &plugin.DeliveryError{Code: plugin.ErrCodeDeliveryAuth, Err: errors.New("bad token " + env.Config["token"].(string))}
	}
	if n, _ := env.Config["fail_times"].(int64); failures[name] < int(n) {
		failures[name]++
		return plugin.SendResult{}, &plugin.DeliveryError{Code: plugin.ErrCodeDeliveryFailed, Retry: true, Err: errors.New("try later")}
	}
	files := map[string]string{}
	for _, a := range msg.Attachments {
		files[a.Name] = string(destinationtest.ReadAll(a))
	}
	inbox = append(inbox, received{channel: name, msg: msg, files: files})
	return plugin.SendResult{Meta: map[string]any{"id": len(inbox)}}, nil
}

func (fakeDest) Test(context.Context, plugin.DestinationEnv) error { return nil }
func (fakeDest) Preview(_ context.Context, _ plugin.DestinationEnv, msg plugin.Message) (plugin.Preview, error) {
	return plugin.Preview{Body: msg.Inline, BodyType: "html"}, nil
}

func init() {
	plugin.Register(plugin.KindDestination, "fakedest", func() plugin.Plugin { return fakeDest{} })
}
