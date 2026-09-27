// Package xlsx is the Excel formatter: typed cells, a bold frozen header, an autofilter, column
// widths fitted to the content and a sheet named after the report (docs/spec/04-plugins.md).
//
// Excel keeps numbers as doubles, which hold 15 significant digits. Integers and decimals within
// that are written as numbers (the text stored in the file is exactly the decimal's value);
// longer ones are written as text so that no digit is lost.
package xlsx

import (
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"

	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the plugin id.
const ID = "xlsx"

const (
	widthSample = 100
	minWidth    = 8
	maxWidth    = 60
	maxDigits   = 15
)

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MustLoadMessages(locales)

func init() {
	plugin.Register(plugin.KindFormatter, ID, func() plugin.Plugin { return formatter{} })
}

type formatter struct{}

func (formatter) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.format.xlsx.name", Description: "plugin.format.xlsx.description", Icon: "xlsx", Version: "1.0.0"}
}

func (formatter) ConfigSchema() *plugin.Schema { return &plugin.Schema{} }

func (formatter) Capabilities() any {
	return plugin.FormatterCapabilities{Kind: plugin.FormatterFile, ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", Extension: "xlsx"}
}

func (formatter) Messages() plugin.Messages { return messages }

func (formatter) Format(ctx context.Context, in plugin.FormatInput, w io.Writer) (plugin.FormatResult, error) {
	var res plugin.FormatResult
	if err := ctx.Err(); err != nil {
		return res, err
	}
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := SheetName(in.Title)
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return res, err
	}
	created := in.GeneratedAt.UTC().Format(time.RFC3339)
	if err := f.SetDocProps(&excelize.DocProperties{Title: in.Title, Creator: "Rowbird", Created: created, Modified: created}); err != nil {
		return res, err
	}
	s := &styles{f: f, cache: map[string]int{}, pt: in.Locale == "pt-BR"}
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return res, err
	}

	// The first rows decide the column widths; a stream cannot change them afterwards.
	var head [][]any
	for len(head) < widthSample && in.Rows.Next() {
		head = append(head, in.Rows.Row())
	}
	if err := in.Rows.Err(); err != nil {
		return res, err
	}
	for i, c := range in.Columns {
		width := utf8.RuneCountInString(c.Name) + 2
		for _, row := range head {
			if i < len(row) {
				width = max(width, utf8.RuneCountInString(format.Text(c, row[i], in.Locale, in.Location))+2)
			}
		}
		if err := sw.SetColWidth(i+1, i+1, float64(min(max(width, minWidth), maxWidth))); err != nil {
			return res, err
		}
	}
	if len(in.Columns) > 0 {
		if err := sw.SetPanes(&excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
			return res, err
		}
	}
	bold, err := s.get("header", &excelize.Style{Font: &excelize.Font{Bold: true}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"E7E9EE"}}})
	if err != nil {
		return res, err
	}
	header := make([]any, len(in.Columns))
	for i, c := range in.Columns {
		header[i] = excelize.Cell{StyleID: bold, Value: format.Neutralize(c.Name)}
	}
	if err := sw.SetRow("A1", header); err != nil {
		return res, err
	}
	write := func(row []any) error {
		if res.Rows%1000 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		cells := make([]any, len(in.Columns))
		for i, c := range in.Columns {
			if i < len(row) {
				v, err := s.cell(c, row[i], in.Location)
				if err != nil {
					return err
				}
				cells[i] = v
			}
		}
		addr, _ := excelize.CoordinatesToCellName(1, int(res.Rows)+2)
		res.Rows++
		return sw.SetRow(addr, cells)
	}
	for _, row := range head {
		if err := write(row); err != nil {
			return res, err
		}
	}
	for in.Rows.Next() {
		if err := write(in.Rows.Row()); err != nil {
			return res, err
		}
	}
	if err := in.Rows.Err(); err != nil {
		return res, err
	}
	if err := sw.Flush(); err != nil {
		return res, err
	}
	if len(in.Columns) > 0 {
		last, _ := excelize.CoordinatesToCellName(len(in.Columns), int(res.Rows)+1)
		if err := f.AutoFilter(sheet, "A1:"+last, nil); err != nil {
			return res, err
		}
	}
	return res, f.Write(w)
}

// SheetName makes a valid sheet name from a title: at most 31 characters, none of : \ / ? * [ ].
func SheetName(title string) string {
	name := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`:\/?*[]`, r) || r < ' ' {
			return ' '
		}
		return r
	}, title)
	name = strings.Trim(strings.TrimSpace(name), "'")
	if r := []rune(name); len(r) > 31 {
		name = strings.TrimSpace(string(r[:31]))
	}
	if name == "" {
		return "Result"
	}
	return name
}

type styles struct {
	f     *excelize.File
	cache map[string]int
	pt    bool
}

func (s *styles) get(key string, st *excelize.Style) (int, error) {
	if id, ok := s.cache[key]; ok {
		return id, nil
	}
	id, err := s.f.NewStyle(st)
	if err != nil {
		return 0, err
	}
	s.cache[key] = id
	return id, nil
}

func (s *styles) numFmt(code string) (int, error) {
	return s.get("fmt:"+code, &excelize.Style{CustomNumFmt: &code})
}

func (s *styles) cell(col plugin.Column, v any, loc *time.Location) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case int64:
		if n := strconv.FormatInt(x, 10); significant(n) > maxDigits {
			return n, nil
		}
		return x, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return strconv.FormatFloat(x, 'g', -1, 64), nil
		}
		return x, nil
	case plugin.Decimal:
		return s.decimal(string(x))
	case bool:
		return x, nil
	case plugin.Date:
		t, ok := parseDate(string(x))
		if !ok {
			return string(x), nil
		}
		id, err := s.numFmt(map[bool]string{true: "dd/mm/yyyy", false: "yyyy-mm-dd"}[s.pt])
		return excelize.Cell{StyleID: id, Value: t}, err
	case time.Time:
		t := format.Instant(col, x, loc)
		wall := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
		id, err := s.numFmt(map[bool]string{true: "dd/mm/yyyy hh:mm:ss", false: "yyyy-mm-dd hh:mm:ss"}[s.pt])
		return excelize.Cell{StyleID: id, Value: wall}, err
	case plugin.TimeOfDay:
		return string(x), nil
	case plugin.JSON:
		return string(x), nil
	case []byte:
		return base64.StdEncoding.EncodeToString(x), nil
	case string:
		return format.Neutralize(x), nil
	}
	return fmt.Sprint(v), nil
}

// decimal writes a decimal as a number with its scale shown, or as text when Excel cannot hold
// every digit.
func (s *styles) decimal(d string) (any, error) {
	f, ok := parseFloat(d)
	if !ok || significant(d) > maxDigits {
		return d, nil
	}
	scale := 0
	if _, frac, ok := strings.Cut(d, "."); ok {
		scale = len(frac)
	}
	code := "#,##0"
	if scale > 0 {
		code += "." + strings.Repeat("0", scale)
	}
	id, err := s.numFmt(code)
	return excelize.Cell{StyleID: id, Value: f}, err
}

// significant counts the significant digits of a plain decimal string.
func significant(d string) int {
	digits := strings.TrimLeft(strings.NewReplacer("-", "", "+", "", ".", "").Replace(d), "0")
	return len(digits)
}

func parseDate(d string) (time.Time, bool) {
	t, err := time.Parse(time.DateOnly, d)
	return t, err == nil
}

func parseFloat(d string) (float64, bool) {
	f, err := strconv.ParseFloat(d, 64)
	return f, err == nil && !math.IsInf(f, 0)
}
