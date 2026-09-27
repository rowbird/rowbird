package email

import (
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin"
)

func TestAlertEmail(t *testing.T) {
	r := renderAlert("pt-BR", &plugin.MessageAlert{
		Title: "Canal <ops>\nfalhando", Text: "linha 1\nlinha <2>", URL: "https://rb.example.com/channels/c1",
	})
	if r.subject != "Canal <ops> falhando" {
		t.Errorf("subject %q", r.subject)
	}
	for _, want := range []string{"Canal &lt;ops&gt;", "linha 1<br>linha &lt;2&gt;", `href="https://rb.example.com/channels/c1"`, "Abrir no Rowbird"} {
		if !strings.Contains(r.html, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if !strings.Contains(r.text, "Abrir no Rowbird: https://rb.example.com/channels/c1") {
		t.Errorf("text %q", r.text)
	}
}
