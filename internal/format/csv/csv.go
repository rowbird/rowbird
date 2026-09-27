// Package csv is the CSV formatter. Its defaults follow the reader's language so that
// spreadsheets open the file correctly: in Portuguese, a semicolon separator, a UTF-8 byte order
// mark and a decimal comma (docs/spec/04-plugins.md).
package csv

import (
	"context"
	"embed"
	"encoding/base64"
	stdcsv "encoding/csv"
	"io"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the plugin id.
const ID = "csv"

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MustLoadMessages(locales)

func init() {
	plugin.Register(plugin.KindFormatter, ID, func() plugin.Plugin { return formatter{} })
}

type formatter struct{}

func (formatter) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.format.csv.name", Description: "plugin.format.csv.description", Icon: "csv", Version: "1.0.0"}
}

func (formatter) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "delimiter", Type: plugin.TypeString, Default: "auto", Enum: []string{"auto", "comma", "semicolon", "tab", "pipe"}, Label: "plugin.format.csv.delimiter.label", Help: "plugin.format.csv.delimiter.help"},
		{Key: "bom", Type: plugin.TypeString, Default: "auto", Enum: []string{"auto", "yes", "no"}, Label: "plugin.format.csv.bom.label"},
		{Key: "number_format", Type: plugin.TypeString, Default: "locale", Enum: []string{"locale", "plain"}, Label: "plugin.format.csv.number_format.label"},
		{Key: "encoding", Type: plugin.TypeString, Default: "utf-8", Enum: []string{"utf-8", "windows-1252"}, Label: "plugin.format.csv.encoding.label"},
		{Key: "header", Type: plugin.TypeBoolean, Default: true, Label: "plugin.format.csv.header.label"},
	}}
}

func (formatter) Capabilities() any {
	return plugin.FormatterCapabilities{Kind: plugin.FormatterFile, ContentType: "text/csv; charset=utf-8", Extension: "csv"}
}

func (formatter) Messages() plugin.Messages { return messages }

var separators = map[string]rune{"comma": ',', "semicolon": ';', "tab": '\t', "pipe": '|'}

func (formatter) Format(ctx context.Context, in plugin.FormatInput, w io.Writer) (plugin.FormatResult, error) {
	var res plugin.FormatResult
	if err := ctx.Err(); err != nil {
		return res, err
	}
	pt := in.Locale == "pt-BR"
	sep := separators[format.String(in.Options, "delimiter", "auto")]
	if sep == 0 {
		sep = ','
		if pt {
			sep = ';'
		}
	}
	encoding := format.String(in.Options, "encoding", "utf-8")
	bom := format.String(in.Options, "bom", "auto")
	if encoding == "utf-8" && (bom == "yes" || (bom == "auto" && pt)) {
		if _, err := io.WriteString(w, "\ufeff"); err != nil {
			return res, err
		}
	}
	if encoding == "windows-1252" {
		w = &windows1252{w: w}
	}
	decimal := "."
	if format.String(in.Options, "number_format", "locale") == "locale" {
		decimal = format.GetLocale(in.Locale).Decimal
	}
	cw := stdcsv.NewWriter(w)
	cw.Comma, cw.UseCRLF = sep, true
	if format.Bool(in.Options, "header", true) {
		head := make([]string, len(in.Columns))
		for i, c := range in.Columns {
			head[i] = format.Neutralize(c.Name)
		}
		if err := cw.Write(head); err != nil {
			return res, err
		}
	}
	record := make([]string, len(in.Columns))
	for in.Rows.Next() {
		if res.Rows%1000 == 0 {
			if err := ctx.Err(); err != nil {
				return res, err
			}
		}
		row := in.Rows.Row()
		for i := range record {
			record[i] = ""
			if i < len(row) {
				record[i] = cell(in.Columns[i], row[i], decimal, in.Location)
			}
		}
		if err := cw.Write(record); err != nil {
			return res, err
		}
		res.Rows++
	}
	if err := in.Rows.Err(); err != nil {
		return res, err
	}
	cw.Flush()
	return res, cw.Error()
}

// cell writes a value in a form spreadsheets read back: numbers with the chosen decimal separator
// and no grouping, ISO dates, text with formulas neutralized.
func cell(col plugin.Column, v any, decimal string, loc *time.Location) string {
	if n, ok := format.NumberText(v); ok {
		if decimal == "." {
			return n
		}
		return format.Locale{Decimal: decimal}.Number(n, false)
	}
	switch x := v.(type) {
	case nil:
		return ""
	case bool:
		if x {
			return "true"
		}
		return "false"
	case plugin.Date:
		return string(x)
	case time.Time:
		t := format.Instant(col, x, loc)
		if t.Nanosecond() != 0 {
			return t.Format("2006-01-02 15:04:05.999999")
		}
		return t.Format(time.DateTime)
	case plugin.TimeOfDay:
		return string(x)
	case plugin.JSON:
		return string(x)
	case []byte:
		return base64.StdEncoding.EncodeToString(x)
	case string:
		return format.Neutralize(x)
	}
	return ""
}

// windows1252 re-encodes the UTF-8 the CSV writer produces. Characters Windows-1252 cannot
// represent become "?". A rune split across two writes is held until it is complete.
type windows1252 struct {
	w       io.Writer
	pending []byte
}

func (e *windows1252) Write(p []byte) (int, error) {
	buf := append(e.pending, p...)
	out := make([]byte, 0, len(buf))
	for len(buf) > 0 {
		if !utf8.FullRune(buf) {
			break
		}
		r, size := utf8.DecodeRune(buf)
		b, ok := charmap.Windows1252.EncodeRune(r)
		if !ok {
			b = '?'
		}
		out = append(out, b)
		buf = buf[size:]
	}
	e.pending = append(e.pending[:0], buf...)
	if _, err := e.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}
