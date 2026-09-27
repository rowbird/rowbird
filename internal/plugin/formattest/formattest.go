// Package formattest is the conformance suite every formatter must pass: metadata and
// translations, every value type including NULL, unicode and hostile text, determinism, context
// cancellation, the limits of inline outputs, and golden files in English and Portuguese.
package formattest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/format/golden"
	"github.com/rowbird/rowbird/internal/plugin"
)

// Locales are the languages every golden file exists in.
var Locales = []string{"en", "pt-BR"}

// Harness describes how to check one formatter.
type Harness struct {
	Formatter plugin.Formatter
	// Dir holds the golden files, usually "testdata/golden".
	Dir string
	// Dump turns binary output (XLSX, PDF) into comparable text; nil compares the bytes.
	Dump func(t *testing.T, out []byte) []byte
	// Options are extra option sets, each checked against its own golden files.
	Options map[string]map[string]any
}

// Hostile is text that must come out harmless: HTML, spreadsheet formulas and control characters.
const Hostile = `<script>alert("x")</script> & 'quotes'`

// Columns of the sample result: every normalized type.
func Columns() []plugin.Column {
	return []plugin.Column{
		{Name: "id", Type: plugin.TypeInt, DBType: "INTEGER"},
		{Name: "região", Type: plugin.TypeText, DBType: "TEXT"},
		{Name: "total", Type: plugin.TypeDecimal, DBType: "NUMERIC(38,2)"},
		{Name: "ratio", Type: plugin.TypeFloat, DBType: "DOUBLE"},
		{Name: "active", Type: plugin.TypeBool, DBType: "BOOLEAN"},
		{Name: "day", Type: plugin.TypeDate, DBType: "DATE"},
		{Name: "created_at", Type: plugin.TypeDateTime, DBType: "TIMESTAMPTZ", WithTimeZone: true},
		{Name: "at", Type: plugin.TypeTime, DBType: "TIME"},
		{Name: "payload", Type: plugin.TypeJSON, DBType: "JSONB"},
		{Name: "raw", Type: plugin.TypeBinary, DBType: "BYTEA"},
	}
}

// Rows of the sample result: ordinary values, extremes, hostile text and a row of NULLs.
func Rows() [][]any {
	at := time.Date(2026, 9, 25, 13, 30, 0, 0, time.UTC)
	return [][]any{
		{int64(1), "São Paulo", plugin.Decimal("1234.50"), 0.25, true, plugin.Date("2026-09-25"), at, plugin.TimeOfDay("08:30:00"), plugin.JSON(`{"a":1}`), []byte{0xde, 0xad}},
		{int64(9007199254740993), "=HYPERLINK(\"http://x\")", plugin.Decimal("-12345678901234567890123456789012345.67"), -1.5, false, plugin.Date("2024-02-29"), at.Add(26*time.Hour + 5*time.Second), plugin.TimeOfDay("23:59:59"), plugin.JSON(`[]`), []byte{}},
		{int64(-3), Hostile, plugin.Decimal("0.00"), 1e-7, true, plugin.Date("1999-12-31"), time.Date(1999, 12, 31, 23, 59, 0, 0, time.UTC), plugin.TimeOfDay("00:00:00"), plugin.JSON(`{"quote":"\""}`), []byte("x")},
		{int64(4), "line one\nline two, with ; and \"", plugin.Decimal("7"), 3.0, false, nil, nil, nil, nil, nil},
		{nil, nil, nil, nil, nil, nil, nil, nil, nil, nil},
	}
}

// Input builds the sample input in locale.
func Input(locale string, rows [][]any, cols []plugin.Column) plugin.FormatInput {
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	return plugin.FormatInput{
		Columns: cols, Rows: format.SliceRows(rows), RowCount: int64(len(rows)), Locale: locale, Location: loc,
		Title: "Vendas por região", RunID: "01900000-0000-7000-8000-000000000001",
		GeneratedAt: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC), Options: map[string]any{},
	}
}

// Many returns n simple rows, for paging and inline limits.
func Many(n int) ([]plugin.Column, [][]any) {
	cols := []plugin.Column{{Name: "n", Type: plugin.TypeInt}, {Name: "name", Type: plugin.TypeText}, {Name: "amount", Type: plugin.TypeDecimal}}
	rows := make([][]any, n)
	for i := range rows {
		rows[i] = []any{int64(i + 1), fmt.Sprintf("Customer %03d", i+1), plugin.Decimal(fmt.Sprintf("%d.%02d", (i+1)*137, i%100))}
	}
	return cols, rows
}

// Wide returns a result with nine columns (landscape documents, wide tables).
func Wide() ([]plugin.Column, [][]any) {
	var cols []plugin.Column
	row := []any{}
	for i := range 9 {
		cols = append(cols, plugin.Column{Name: fmt.Sprintf("column_%d", i+1), Type: plugin.TypeText})
		row = append(row, strings.Repeat("value ", i+1))
	}
	return cols, [][]any{row, row}
}

type goldenCase struct {
	name string
	in   func(locale string) plugin.FormatInput
}

func cases() []goldenCase {
	mcols, mrows := Many(150)
	wcols, wrows := Wide()
	return []goldenCase{
		{"sample", func(l string) plugin.FormatInput { return Input(l, Rows(), Columns()) }},
		{"empty", func(l string) plugin.FormatInput { return Input(l, nil, Columns()) }},
		{"wide", func(l string) plugin.FormatInput { return Input(l, wrows, wcols) }},
		{"many", func(l string) plugin.FormatInput {
			in := Input(l, mrows, mcols)
			in.RowCount, in.Truncated = 1000, true
			in.MaxRows, in.MaxChars, in.LinkURL = 20, 4000, "https://rowbird.example.com/runs/01900000-0000-7000-8000-000000000001"
			return in
		}},
	}
}

// Run executes the suite.
func Run(t *testing.T, h Harness) {
	f := h.Formatter
	caps, _ := f.Capabilities().(plugin.FormatterCapabilities)
	inline := caps.Kind == plugin.FormatterInline
	ext := caps.Extension
	if inline {
		ext = map[string]string{plugin.InlineHTML: "html", plugin.InlineMarkdown: "md", plugin.InlineText: "txt"}[caps.InlineTarget]
	}
	if h.Dump != nil {
		ext += ".txt"
	}
	options := func(extra map[string]any) map[string]any {
		values, err := f.ConfigSchema().Validate(extra)
		if err != nil {
			t.Fatalf("options %v do not validate: %v", extra, err)
		}
		return values
	}
	render := func(t *testing.T, ctx context.Context, in plugin.FormatInput) ([]byte, plugin.FormatResult) {
		t.Helper()
		var buf bytes.Buffer
		res, err := f.Format(ctx, in, &buf)
		if err != nil {
			t.Fatalf("format: %v", err)
		}
		out := buf.Bytes()
		if h.Dump != nil {
			out = h.Dump(t, out)
		}
		return out, res
	}

	t.Run("metadata", func(t *testing.T) {
		m := f.Meta()
		if m.ID == "" || m.Version == "" || f.ConfigSchema() == nil {
			t.Fatal("metadata or schema missing")
		}
		switch {
		case caps.Kind == plugin.FormatterFile && (caps.Extension == "" || caps.ContentType == ""):
			t.Error("file formatters declare an extension and a content type")
		case inline && caps.InlineTarget == "":
			t.Error("inline formatters declare their target")
		case !inline && caps.Kind != plugin.FormatterFile:
			t.Errorf("unknown kind %q", caps.Kind)
		}
		msgs := f.Messages()
		keys := []string{m.Name, m.Description}
		for _, fld := range f.ConfigSchema().Fields {
			keys = append(keys, fld.Label)
			for _, v := range fld.Enum {
				keys = append(keys, strings.TrimSuffix(fld.Label, ".label")+"."+v)
			}
		}
		for _, locale := range Locales {
			for _, k := range keys {
				if msgs[locale][k] == "" {
					t.Errorf("%s has no %s translation for %s", m.ID, locale, k)
				}
			}
		}
	})

	sets := map[string]map[string]any{"": {}}
	for name, o := range h.Options {
		sets["."+name] = o
	}
	for _, c := range cases() {
		for suffix, extra := range sets {
			for _, locale := range Locales {
				t.Run(fmt.Sprintf("golden %s%s %s", c.name, suffix, locale), func(t *testing.T) {
					in := c.in(locale)
					in.Options = options(extra)
					out, res := render(t, context.Background(), in)
					golden.Check(t, fmt.Sprintf("%s/%s%s.%s.%s", h.Dir, c.name, suffix, locale, ext), out)

					again := c.in(locale)
					again.Options = in.Options
					if out2, _ := render(t, context.Background(), again); !bytes.Equal(out, out2) {
						t.Error("output is not deterministic")
					}
					checkResult(t, c.name, in, res, out, inline)
				})
			}
		}
	}

	t.Run("hostile text", func(t *testing.T) {
		var buf bytes.Buffer
		if _, err := f.Format(context.Background(), withOptions(Input("en", Rows(), Columns()), options(nil)), &buf); err != nil {
			t.Fatal(err)
		}
		if (caps.InlineTarget == plugin.InlineHTML || caps.InlineTarget == plugin.InlineText) && strings.Contains(buf.String(), "<script>") {
			t.Error("database text reached HTML unescaped")
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		_, rows := Many(5000)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		in := withOptions(Input("en", rows, Columns()[:3]), options(nil))
		_, err := f.Format(ctx, in, &bytes.Buffer{})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	})
}

func withOptions(in plugin.FormatInput, o map[string]any) plugin.FormatInput {
	in.Options = o
	return in
}

func checkResult(t *testing.T, name string, in plugin.FormatInput, res plugin.FormatResult, out []byte, inline bool) {
	t.Helper()
	total := map[string]int64{"sample": 5, "empty": 0, "wide": 2, "many": 150}[name]
	switch {
	case !inline && res.Rows != total:
		t.Errorf("wrote %d rows, want %d", res.Rows, total)
	case inline && in.MaxRows > 0 && res.Rows > int64(in.MaxRows):
		t.Errorf("inline output has %d rows, above the limit of %d", res.Rows, in.MaxRows)
	case inline && name == "many" && !res.Cut:
		t.Error("inline output of a long result must say it was cut")
	}
	if inline && in.MaxChars > 0 && len([]rune(string(out))) > in.MaxChars {
		t.Errorf("inline output has %d characters, above the limit of %d", len([]rune(string(out))), in.MaxChars)
	}
	if inline && name == "many" && in.LinkURL != "" && !strings.Contains(string(out), in.LinkURL) {
		t.Error("inline output of a cut result must link to the full report")
	}
}
