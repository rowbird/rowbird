package uptimekuma

import (
	"net/url"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/destinationtest"
)

func TestAlertStatus(t *testing.T) {
	h := destinationtest.Harness{Destination: dest{}, Config: map[string]any{"push_url": "https://kuma.example.com/api/push/x"}}
	for _, c := range []struct {
		alert plugin.MessageAlert
		want  string
	}{
		{plugin.MessageAlert{Title: "Channel failing", Severity: plugin.AlertError}, "down"},
		{plugin.MessageAlert{Title: "Channel recovered", Severity: plugin.AlertError, Recovered: true}, "up"},
		{plugin.MessageAlert{Title: "Report paused", Severity: plugin.AlertWarning}, "up"},
	} {
		msg := destinationtest.Message("en")
		msg.Alert = &c.alert
		raw, status, err := pushURL(h.Env(t), msg)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(raw)
		if status != c.want || u.Query().Get("status") != c.want || u.Query().Get("msg") != c.alert.Title {
			t.Errorf("%s: %s", c.alert.Title, raw)
		}
	}
}
