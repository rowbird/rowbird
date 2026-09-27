package inline

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/formattest"
)

func get(t *testing.T, id string) plugin.Formatter {
	t.Helper()
	p, ok := plugin.Get(plugin.KindFormatter, id)
	if !ok {
		t.Fatalf("%s is not registered", id)
	}
	return p.(plugin.Formatter)
}

func TestConformance(t *testing.T) {
	for _, id := range []string{IDHTML, IDMarkdown, IDText} {
		t.Run(id, func(t *testing.T) {
			formattest.Run(t, formattest.Harness{Formatter: get(t, id), Dir: "testdata/golden/" + id})
		})
	}
}

func render(t *testing.T, id string, in plugin.FormatInput) (string, plugin.FormatResult) {
	t.Helper()
	var buf bytes.Buffer
	res, err := get(t, id).Format(context.Background(), in, &buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf.String(), res
}

func TestCharacterLimit(t *testing.T) {
	cols, rows := formattest.Many(150)
	for _, id := range []string{IDHTML, IDMarkdown, IDText} {
		for _, limit := range []int{4096, 2000, 700, 120} {
			in := formattest.Input("en", rows, cols)
			in.MaxChars, in.LinkURL = limit, "https://rowbird.example.com/runs/1"
			out, res := render(t, id, in)
			if n := utf8.RuneCountInString(out); n > limit {
				t.Errorf("%s limit %d: %d characters", id, limit, n)
			}
			if !res.Cut || res.Rows >= 150 {
				t.Errorf("%s limit %d: rows %d cut %v", id, limit, res.Rows, res.Cut)
			}
			// HTML markup is verbose: a few hundred characters do not hold a row.
			roomy := limit >= 700 && (id != IDHTML || limit >= 2000)
			if roomy && (res.Rows == 0 || !strings.Contains(out, "https://rowbird.example.com/runs/1")) {
				t.Errorf("%s limit %d: %d rows, output %q", id, limit, res.Rows, out)
			}
		}
	}
}

func TestUnsafeLinksAreDropped(t *testing.T) {
	cols, rows := formattest.Many(30)
	for _, link := range []string{"javascript:alert(1)", "data:text/html,x", "//evil.example.com", "not a url"} {
		in := formattest.Input("en", rows, cols)
		in.MaxRows, in.LinkURL = 5, link
		out, _ := render(t, IDHTML, in)
		if strings.Contains(out, "href") {
			t.Errorf("%q produced a link: %s", link, out)
		}
	}
}

func TestMarkdownFallsBackToCodeBlock(t *testing.T) {
	cols, rows := formattest.Wide()
	out, _ := render(t, IDMarkdown, formattest.Input("en", rows, cols))
	if !strings.HasPrefix(out, "```\n") {
		t.Errorf("wide table not in a code block: %q", out)
	}
	cols, rows = formattest.Many(2)
	out, _ = render(t, IDMarkdown, formattest.Input("pt-BR", rows, cols))
	if !strings.HasPrefix(out, "| n | name | amount |\n| ---: | --- | ---: |\n") {
		t.Errorf("narrow table %q", out)
	}
}
