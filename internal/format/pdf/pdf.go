// Package pdf is the PDF formatter, built with maroto (pure Go, ADR-0011): the report's title and
// when it was generated, a table whose header repeats on every page, page numbers, landscape pages
// for wide results and an optional logo (docs/spec/04-plugins.md).
//
// Text uses the Go fonts, embedded, so every Latin accent prints without depending on the host.
// A PDF of a huge result is not useful and costs memory, so at most MaxRows rows are printed and
// the document says how many there were.
package pdf

import (
	"context"
	"embed"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/image"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/breakline"
	"github.com/johnfercher/maroto/v2/pkg/consts/extension"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/orientation"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the plugin id.
const ID = "pdf"

// Layout.
const (
	MaxRows          = 5000
	landscapeColumns = 6
	// grid is the number of units a row is divided into; results with more columns get one unit
	// per column.
	grid        = 100
	family      = "go"
	fontSize    = 8
	widthSample = 50
)

var (
	headerFill = &props.Color{Red: 231, Green: 233, Blue: 238}
	zebraFill  = &props.Color{Red: 246, Green: 247, Blue: 249}
	muted      = &props.Color{Red: 100, Green: 100, Blue: 110}
)

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MustLoadMessages(locales)

func init() {
	plugin.Register(plugin.KindFormatter, ID, func() plugin.Plugin { return formatter{} })
}

type formatter struct{}

func (formatter) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.format.pdf.name", Description: "plugin.format.pdf.description", Icon: "pdf", Version: "1.0.0"}
}

func (formatter) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "orientation", Type: plugin.TypeString, Default: "auto", Enum: []string{"auto", "portrait", "landscape"}, Label: "plugin.format.pdf.orientation.label", Help: "plugin.format.pdf.orientation.help"},
	}}
}

func (formatter) Capabilities() any {
	return plugin.FormatterCapabilities{Kind: plugin.FormatterFile, ContentType: "application/pdf", Extension: "pdf"}
}

func (formatter) Messages() plugin.Messages { return messages }

func (formatter) Format(ctx context.Context, in plugin.FormatInput, w io.Writer) (plugin.FormatResult, error) {
	m, res, err := Document(ctx, in)
	if err != nil {
		return res, err
	}
	doc, err := m.Generate()
	if err != nil {
		return res, err
	}
	_, err = w.Write(doc.GetBytes())
	return res, err
}

// Document builds the document without rendering it; tests compare its structure.
func Document(ctx context.Context, in plugin.FormatInput) (core.Maroto, plugin.FormatResult, error) {
	var res plugin.FormatResult
	if err := ctx.Err(); err != nil {
		return nil, res, err
	}
	fonts, err := fontrepository.New().
		AddUTF8FontFromBytes(family, fontstyle.Normal, goregular.TTF).
		AddUTF8FontFromBytes(family, fontstyle.Bold, gobold.TTF).
		Load()
	if err != nil {
		return nil, res, err
	}
	orient := orientation.Vertical
	switch format.String(in.Options, "orientation", "auto") {
	case "landscape":
		orient = orientation.Horizontal
	case "auto":
		if len(in.Columns) > landscapeColumns {
			orient = orientation.Horizontal
		}
	}
	cfg := config.NewBuilder().
		WithPageSize(pagesize.A4).WithOrientation(orient).
		WithLeftMargin(10).WithRightMargin(10).WithTopMargin(10).
		WithCustomFonts(fonts).WithDefaultFont(&props.Font{Family: family, Size: fontSize}).
		WithMaxGridSize(gridSize(len(in.Columns))).
		WithPageNumber(props.PageNumber{
			Pattern: format.T(in.Locale, "format.page", map[string]any{"n": "{current}", "total": "{total}"}),
			Place:   props.RightBottom, Family: family, Size: 7, Color: muted,
		}).
		WithTitle(in.Title, true).WithCreator("Rowbird", true).WithCreationDate(in.GeneratedAt).
		WithSequentialMode().
		Build()
	m := maroto.New(cfg)

	// Rows are read first: the widths depend on the content.
	var rows [][]string
	for in.Rows.Next() {
		if len(rows)%500 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, res, err
			}
		}
		if len(rows) == MaxRows {
			continue
		}
		raw := in.Rows.Row()
		cells := make([]string, len(in.Columns))
		for i, c := range in.Columns {
			if i < len(raw) {
				cells[i] = flatten.Replace(format.Text(c, raw[i], in.Locale, in.Location))
			}
		}
		rows = append(rows, cells)
	}
	if err := in.Rows.Err(); err != nil {
		return nil, res, err
	}
	res.Rows = int64(len(rows))
	sizes := widths(in.Columns, rows)
	right := make([]bool, len(in.Columns))
	for i, c := range in.Columns {
		right[i] = c.Type == plugin.TypeInt || c.Type == plugin.TypeDecimal || c.Type == plugin.TypeFloat
	}

	var title []core.Col
	if ext, ok := imageType(in.Logo); ok {
		title = append(title, image.NewFromBytesCol(12, in.Logo, ext))
	}
	title = append(title, col.New(gridSize(len(in.Columns))-len(title)*12).Add(
		text.New(in.Title, props.Text{Size: 14, Style: fontstyle.Bold, Family: family}),
		text.New(format.T(in.Locale, "format.generatedAt", map[string]any{"time": format.GetLocale(in.Locale).FormatDateTime(in.GeneratedAt.In(location(in)))}),
			props.Text{Top: 7, Size: fontSize, Color: muted, Family: family}),
	))
	headerCells := make([]core.Col, len(in.Columns))
	for i, c := range in.Columns {
		headerCells[i] = text.NewCol(sizes[i], c.Name, cellText(right[i], fontstyle.Bold))
	}
	if err := m.RegisterHeader(
		row.New(16).Add(title...),
		row.New().Add(headerCells...).WithStyle(&props.Cell{BackgroundColor: headerFill}),
	); err != nil {
		return nil, res, err
	}

	if len(rows) == 0 {
		m.AddRows(text.NewAutoRow(format.T(in.Locale, "format.noRows", nil), props.Text{Top: 2, Family: family, Color: muted}))
	}
	for n, cells := range rows {
		cols := make([]core.Col, len(cells))
		for i, v := range cells {
			cols[i] = text.NewCol(sizes[i], v, cellText(right[i], fontstyle.Normal))
		}
		r := row.New().Add(cols...)
		if n%2 == 1 {
			r = r.WithStyle(&props.Cell{BackgroundColor: zebraFill})
		}
		m.AddRows(r)
	}
	if total := max(in.RowCount, res.Rows); total > res.Rows || in.Truncated {
		note := format.T(in.Locale, "format.showing", map[string]any{"shown": res.Rows, "total": format.GetLocale(in.Locale).Number(itoa(total), true)})
		if in.Truncated {
			note += " " + format.T(in.Locale, "format.limitCut", nil)
		}
		m.AddRows(text.NewAutoRow(note, props.Text{Top: 3, Family: family, Color: muted}))
	}
	return m, res, nil
}

// flatten keeps a cell on one logical line; the table wraps it to the column's width.
var flatten = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")

func cellText(right bool, style fontstyle.Type) props.Text {
	p := props.Text{
		Top: 1.5, Bottom: 1.5, Left: 1, Right: 1.5, Family: family, Size: fontSize, Style: style, Align: align.Left,
		BreakLineStrategy: breakline.DashStrategy,
	}
	if right {
		p.Align = align.Right
	}
	return p
}

// widths splits the grid among columns by the length of their content (header and the first
// rows), so that every column gets at least a little room.
func widths(cols []plugin.Column, rows [][]string) []int {
	n := len(cols)
	if n == 0 {
		return nil
	}
	weights := make([]int, n)
	total := 0
	for i, c := range cols {
		w := utf8.RuneCountInString(c.Name)
		for _, r := range rows[:min(len(rows), widthSample)] {
			w = max(w, utf8.RuneCountInString(r[i]))
		}
		// A little room beyond the text keeps short unbreakable values (dates) off their
		// neighbours; long text is capped and wraps.
		w = min(max(w, 4), 30) + 3
		weights[i] = w
		total += w
	}
	g := gridSize(n)
	sizes := make([]int, n)
	used := 0
	for i := range weights {
		sizes[i] = max(1, weights[i]*g/total)
		used += sizes[i]
	}
	// Spread the rounding rest over the columns, or take it back from the wider ones. The grid has
	// at least one unit per column, so this ends.
	for i := 0; used != g; i = (i + 1) % n {
		switch {
		case used < g:
			sizes[i]++
			used++
		case sizes[i] > 1:
			sizes[i]--
			used--
		}
	}
	return sizes
}

func gridSize(columns int) int { return max(grid, columns) }

func imageType(b []byte) (extension.Type, bool) {
	if len(b) == 0 {
		return "", false
	}
	switch http.DetectContentType(b) {
	case "image/png":
		return extension.Png, true
	case "image/jpeg":
		return extension.Jpg, true
	}
	return "", false
}

func location(in plugin.FormatInput) *time.Location {
	if in.Location != nil {
		return in.Location
	}
	return time.UTC
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
