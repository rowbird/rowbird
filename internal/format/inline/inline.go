// Package inline holds the formatters that put a result in the body of a message: an email-safe
// HTML table, a Markdown table for Slack and Discord and preformatted text for Telegram
// (docs/spec/04-plugins.md). They show at most MaxRows rows, never exceed MaxChars, and end with
// "showing N of M" and a link to the full report when something was left out.
package inline

import (
	"context"
	"embed"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/plugin"
)

// Plugin ids.
const (
	IDHTML     = "html_table"
	IDMarkdown = "markdown_table"
	IDText     = "text"
)

// maxCell bounds a cell in the aligned text layouts.
const maxCell = 40

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MustLoadMessages(locales)

func init() {
	for _, f := range []formatter{
		{id: IDHTML, target: plugin.InlineHTML, contentType: "text/html; charset=utf-8", render: renderHTML},
		{id: IDMarkdown, target: plugin.InlineMarkdown, contentType: "text/markdown; charset=utf-8", render: renderMarkdown},
		{id: IDText, target: plugin.InlineText, contentType: "text/html; charset=utf-8", render: renderText},
	} {
		plugin.Register(plugin.KindFormatter, f.id, func() plugin.Plugin { return f })
	}
}

// table is the part of the result a message shows, as text cells.
type table struct {
	cols  []plugin.Column
	names []string
	rows  [][]string
	// total is how many rows the result has; limitCut says the row limit cut the result itself.
	total    int64
	limitCut bool
	locale   string
	link     string
}

// shown says whether rows were left out.
func (t *table) partial() bool { return int64(len(t.rows)) < t.total || t.limitCut }

// footer is the closing line: how much is shown and where the rest is.
func (t *table) footer(link func(text, href string) string) string {
	if len(t.rows) == 0 && t.total == 0 {
		return format.T(t.locale, "format.noRows", nil)
	}
	if !t.partial() {
		return ""
	}
	l := format.GetLocale(t.locale)
	s := format.T(t.locale, "format.showing", map[string]any{
		"shown": l.Number(strconv.Itoa(len(t.rows)), true), "total": l.Number(strconv.FormatInt(t.total, 10), true),
	})
	if t.limitCut {
		s += " " + format.T(t.locale, "format.limitCut", nil)
	}
	if t.link != "" {
		s += " " + link(format.T(t.locale, "format.viewFull", nil), t.link)
	}
	return s
}

// renderer writes a table; fit reports whether a candidate output fits the character limit.
type renderer func(t *table, fit func(string) bool) (string, int)

type formatter struct {
	id, target, contentType string
	render                  renderer
}

func (f formatter) Meta() plugin.Metadata {
	return plugin.Metadata{ID: f.id, Name: "plugin.format." + f.id + ".name", Description: "plugin.format." + f.id + ".description", Icon: "table", Version: "1.0.0"}
}

func (formatter) ConfigSchema() *plugin.Schema { return &plugin.Schema{} }

func (f formatter) Capabilities() any {
	return plugin.FormatterCapabilities{Kind: plugin.FormatterInline, ContentType: f.contentType, InlineTarget: f.target}
}

func (formatter) Messages() plugin.Messages { return messages }

func (f formatter) Format(ctx context.Context, in plugin.FormatInput, w io.Writer) (plugin.FormatResult, error) {
	var res plugin.FormatResult
	if err := ctx.Err(); err != nil {
		return res, err
	}
	t := &table{cols: in.Columns, total: in.RowCount, limitCut: in.Truncated, locale: in.Locale, link: safeLink(in.LinkURL)}
	for _, c := range in.Columns {
		t.names = append(t.names, c.Name)
	}
	var read int64
	for in.Rows.Next() {
		if read%1000 == 0 {
			if err := ctx.Err(); err != nil {
				return res, err
			}
		}
		read++
		if in.MaxRows > 0 && len(t.rows) >= in.MaxRows {
			continue
		}
		raw := in.Rows.Row()
		cells := make([]string, len(in.Columns))
		for i, c := range in.Columns {
			if i < len(raw) {
				cells[i] = format.Text(c, raw[i], in.Locale, in.Location)
			}
		}
		t.rows = append(t.rows, cells)
	}
	if err := in.Rows.Err(); err != nil {
		return res, err
	}
	t.total = max(t.total, read)
	fit := func(s string) bool { return in.MaxChars <= 0 || utf8.RuneCountInString(s) <= in.MaxChars }
	out, shown := f.render(t, fit)
	res.Rows, res.Cut = int64(shown), int64(shown) < t.total || t.limitCut
	_, err := io.WriteString(w, out)
	return res, err
}

// fitRows finds how many rows fit: the largest n whose rendering fits, found by bisection since
// the rendering grows with n. It returns the output for that n.
func fitRows(t *table, build func(n int) string, fit func(string) bool) (string, int) {
	all := t.rows
	render := func(n int) string {
		t.rows = all[:n]
		return build(n)
	}
	if out := render(len(all)); fit(out) {
		return out, len(all)
	}
	lo, hi := 0, len(all)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fit(render(mid)) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	out := render(lo)
	if !fit(out) {
		// Not even the header fits: the footer alone, cut if it has to be.
		t.rows = nil
		out = clip(t.footer(func(text, _ string) string { return text }), fit)
	}
	return out, len(t.rows)
}

func clip(s string, fit func(string) bool) string {
	r := []rune(s)
	for len(r) > 0 && !fit(string(r)) {
		r = r[:len(r)-1]
	}
	return string(r)
}

// safeLink accepts only absolute http(s) links, so a message never carries a script URL.
func safeLink(s string) string {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.String()
}

// numeric says whether a column is right-aligned.
func numeric(c plugin.Column) bool {
	return c.Type == plugin.TypeInt || c.Type == plugin.TypeDecimal || c.Type == plugin.TypeFloat
}

// flat keeps a cell on one line.
var flat = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")

// grid lays the shown rows out as aligned monospace text (Markdown code blocks, Telegram).
func grid(t *table) []string {
	width := make([]int, len(t.names))
	cell := func(s string) string {
		s = flat.Replace(s)
		if r := []rune(s); len(r) > maxCell {
			s = string(r[:maxCell-1]) + "…"
		}
		return s
	}
	names := make([]string, len(t.names))
	for i, n := range t.names {
		names[i] = cell(n)
		width[i] = utf8.RuneCountInString(names[i])
	}
	rows := make([][]string, len(t.rows))
	for r, row := range t.rows {
		rows[r] = make([]string, len(row))
		for i, v := range row {
			rows[r][i] = cell(v)
			width[i] = max(width[i], utf8.RuneCountInString(rows[r][i]))
		}
	}
	line := func(cells []string) string {
		parts := make([]string, len(cells))
		for i, c := range cells {
			pad := strings.Repeat(" ", width[i]-utf8.RuneCountInString(c))
			if numeric(t.cols[i]) {
				parts[i] = pad + c
			} else {
				parts[i] = c + pad
			}
		}
		return strings.TrimRight(strings.Join(parts, "  "), " ")
	}
	sep := make([]string, len(width))
	for i, w := range width {
		sep[i] = strings.Repeat("-", w)
	}
	out := []string{line(names), strings.Join(sep, "  ")}
	for _, r := range rows {
		out = append(out, line(r))
	}
	return out
}
