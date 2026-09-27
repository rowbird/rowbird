package csv

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/formattest"
)

func TestConformance(t *testing.T) {
	formattest.Run(t, formattest.Harness{
		Formatter: formatter{}, Dir: "testdata/golden",
		Options: map[string]map[string]any{
			"plain":  {"delimiter": "comma", "number_format": "plain", "bom": "no"},
			"cp1252": {"encoding": "windows-1252", "header": false},
		},
	})
}

func TestDefaultsFollowTheLocale(t *testing.T) {
	render := func(locale string, opts map[string]any) string {
		in := formattest.Input(locale, [][]any{{int64(1), "=1+1", plugin.Decimal("1234.5")}}, formattest.Columns()[:3])
		in.Options = opts
		var buf bytes.Buffer
		if _, err := (formatter{}).Format(context.Background(), in, &buf); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	if got := render("pt-BR", nil); got != "\ufeffid;região;total\r\n1;'=1+1;1234,5\r\n" {
		t.Errorf("pt-BR %q", got)
	}
	if got := render("en", nil); got != "id,região,total\r\n1,'=1+1,1234.5\r\n" {
		t.Errorf("en %q", got)
	}
	if got := render("pt-BR", map[string]any{"encoding": "windows-1252"}); !strings.HasPrefix(got, "id;regi\xe3o;total") {
		t.Errorf("windows-1252 %q", got)
	}
}
