package webhook

import (
	"encoding/json"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/destinationtest"
)

func TestAlertPayload(t *testing.T) {
	h := destinationtest.Harness{Destination: dest{}, Config: map[string]any{"url": "https://hooks.example.com/x"}}
	msg := destinationtest.Message("en")
	msg.Alert = &plugin.MessageAlert{Title: "Channel failing", Text: "delivery.unreachable", Severity: plugin.AlertError}
	event, body, err := encodeBody(h.Env(t), msg)
	if err != nil || event != EventAlert {
		t.Fatalf("%s %v", event, err)
	}
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	alert, _ := got["alert"].(map[string]any)
	if got["event"] != "alert" || alert["title"] != "Channel failing" || alert["severity"] != "error" || got["report"] != nil || got["run"] != nil {
		t.Fatalf("payload %s", body)
	}
}
