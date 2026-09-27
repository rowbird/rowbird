package telegram

import (
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/destinationtest"
)

func TestAlertIsEscaped(t *testing.T) {
	h := destinationtest.Harness{Destination: dest{}, Config: map[string]any{"bot_token": token}, Options: map[string]any{"chat_id": "42"}}
	msg := destinationtest.Message("en")
	msg.Alert = &plugin.MessageAlert{Title: "Channel <ops> & co", Text: "a < b", URL: "https://rb.example.com/channels/c1?a=1&b=2"}
	got, err := dest{}.text(h.Env(t), msg)
	if err != nil {
		t.Fatal(err)
	}
	want := "<b>Channel &lt;ops&gt; &amp; co</b>\n\na &lt; b\n\n" + `<a href="https://rb.example.com/channels/c1?a=1&amp;b=2">Open in Rowbird</a>`
	if got != want {
		t.Fatalf("text:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, msg.Report.Title) {
		t.Error("an alert must not show the report")
	}
}
