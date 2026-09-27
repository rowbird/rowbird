package pdf

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/johnfercher/go-tree/node"
	"github.com/johnfercher/maroto/v2/pkg/core"

	"github.com/rowbird/rowbird/internal/format/golden"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/formattest"
)

var (
	pageRe     = regexp.MustCompile(`/Type /Page\b`)
	mediaboxRe = regexp.MustCompile(`/MediaBox \[([^\]]+)\]`)
)

// summary describes the rendered PDF: its header, pages and page size. The content itself is
// compared through the document structure (TestStructure), since the file embeds fonts.
func summary(t *testing.T, out []byte) []byte {
	t.Helper()
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatal("not a PDF")
	}
	box := ""
	if m := mediaboxRe.FindSubmatch(out); m != nil {
		box = string(m[1])
	}
	return fmt.Appendf(nil, "pages=%d mediabox=[%s]\n", len(pageRe.FindAll(out, -1)), box)
}

func TestConformance(t *testing.T) {
	formattest.Run(t, formattest.Harness{
		Formatter: formatter{}, Dir: "testdata/golden", Dump: summary,
		Options: map[string]map[string]any{"landscape": {"orientation": "landscape"}},
	})
}

// dumpTree writes the component tree: one line per component with its value.
func dumpTree(b *strings.Builder, n *node.Node[core.Structure], depth int) {
	d := n.GetData()
	line := strings.Repeat("  ", depth) + d.Type
	if d.Value != nil && d.Value != "" {
		line += fmt.Sprintf(" %q", fmt.Sprint(d.Value))
	}
	if s, ok := d.Details["prop_align"]; ok && fmt.Sprint(s) == "R" {
		line += " right"
	}
	if s, ok := d.Details["prop_font_style"]; ok && fmt.Sprint(s) == "B" {
		line += " bold"
	}
	b.WriteString(line + "\n")
	for _, c := range n.GetNexts() {
		dumpTree(b, c, depth+1)
	}
}

func TestStructure(t *testing.T) {
	cases := map[string]func(string) plugin.FormatInput{
		"sample": func(l string) plugin.FormatInput { return formattest.Input(l, formattest.Rows(), formattest.Columns()) },
		"empty":  func(l string) plugin.FormatInput { return formattest.Input(l, nil, formattest.Columns()) },
		"cut": func(l string) plugin.FormatInput {
			cols, rows := formattest.Many(3)
			in := formattest.Input(l, rows, cols)
			in.RowCount, in.Truncated = 12000, true
			return in
		},
	}
	for name, mk := range cases {
		for _, locale := range formattest.Locales {
			t.Run(name+" "+locale, func(t *testing.T) {
				in := mk(locale)
				in.Options = map[string]any{"orientation": "auto"}
				m, _, err := Document(context.Background(), in)
				if err != nil {
					t.Fatal(err)
				}
				var b strings.Builder
				dumpTree(&b, m.GetStructure(), 0)
				golden.Check(t, fmt.Sprintf("testdata/golden/structure/%s.%s.txt", name, locale), []byte(b.String()))
			})
		}
	}
}

func TestRowCap(t *testing.T) {
	cols, rows := formattest.Many(MaxRows + 10)
	in := formattest.Input("en", rows, cols)
	_, res, err := Document(context.Background(), in)
	if err != nil || res.Rows != MaxRows {
		t.Fatalf("rows %d %v", res.Rows, err)
	}
}

func TestWidths(t *testing.T) {
	cols := []plugin.Column{{Name: "id"}, {Name: "a long description"}, {Name: "x"}}
	got := widths(cols, [][]string{{"1", strings.Repeat("y", 80), ""}})
	if sum := got[0] + got[1] + got[2]; sum != grid || got[1] <= got[0] || got[2] < 1 {
		t.Errorf("widths %v", got)
	}
	many := make([]plugin.Column, 150)
	for i := range many {
		many[i] = plugin.Column{Name: fmt.Sprint(i)}
	}
	if w := widths(many, nil); len(w) != 150 {
		t.Errorf("widths of 150 columns: %d", len(w))
	}
}
