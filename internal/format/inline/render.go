package inline

import (
	"html"
	"strings"
	"unicode/utf8"
)

// Email clients ignore style sheets, so every element carries its own style.
const (
	fontStyle  = "font-family:Arial,Helvetica,sans-serif;font-size:13px;color:#1f2328"
	thStyle    = "padding:6px 10px;background:#e7e9ee;border-bottom:1px solid #d0d4dc;font-weight:bold;white-space:nowrap"
	tdStyle    = "padding:6px 10px;border-bottom:1px solid #eceef2;vertical-align:top"
	zebra      = "#f6f7f9"
	noteStyle  = "font-family:Arial,Helvetica,sans-serif;font-size:12px;color:#57606a;margin:8px 0 0"
	linkStyle  = "color:#0969da"
	wideMarkup = 100
)

func renderHTML(t *table, fit func(string) bool) (string, int) {
	build := func(int) string {
		var b strings.Builder
		if len(t.names) > 0 {
			b.WriteString(`<table cellpadding="0" cellspacing="0" border="0" style="border-collapse:collapse;` + fontStyle + `">` + "\n<thead><tr>")
			for i, n := range t.names {
				b.WriteString(`<th style="` + thStyle + align(t, i) + `">` + html.EscapeString(n) + "</th>")
			}
			b.WriteString("</tr></thead>\n<tbody>\n")
			for r, row := range t.rows {
				bg := "#ffffff"
				if r%2 == 1 {
					bg = zebra
				}
				b.WriteString(`<tr style="background:` + bg + `">`)
				for i, v := range row {
					b.WriteString(`<td style="` + tdStyle + align(t, i) + `">` + strings.ReplaceAll(html.EscapeString(v), "\n", "<br>") + "</td>")
				}
				b.WriteString("</tr>\n")
			}
			b.WriteString("</tbody>\n</table>\n")
		}
		if f := t.footer(func(text, href string) string {
			return `<a href="` + html.EscapeString(href) + `" style="` + linkStyle + `">` + html.EscapeString(text) + "</a>"
		}); f != "" {
			b.WriteString(`<p style="` + noteStyle + `">` + footerHTML(f) + "</p>\n")
		}
		return b.String()
	}
	return fitRows(t, build, fit)
}

// footerHTML escapes the footer's text but keeps its link, which was built escaped.
func footerHTML(f string) string {
	before, rest, ok := strings.Cut(f, "<a ")
	if !ok {
		return html.EscapeString(f)
	}
	return html.EscapeString(before) + "<a " + rest
}

func align(t *table, i int) string {
	if numeric(t.cols[i]) {
		return ";text-align:right"
	}
	return ";text-align:left"
}

func renderMarkdown(t *table, fit func(string) bool) (string, int) {
	build := func(int) string {
		var b strings.Builder
		if len(t.names) > 0 {
			if wide(t) {
				// Too wide for a table: a code block keeps the columns aligned.
				b.WriteString("```\n")
				for _, l := range grid(t) {
					b.WriteString(strings.ReplaceAll(l, "```", "`\u200b``") + "\n")
				}
				b.WriteString("```\n")
			} else {
				b.WriteString(mdRow(t.names) + "\n")
				sep := make([]string, len(t.names))
				for i := range sep {
					sep[i] = "---"
					if numeric(t.cols[i]) {
						sep[i] = "---:"
					}
				}
				b.WriteString("| " + strings.Join(sep, " | ") + " |\n")
				for _, row := range t.rows {
					b.WriteString(mdRow(row) + "\n")
				}
			}
		}
		if f := t.footer(func(text, href string) string { return "[" + mdEscape(text) + "](" + href + ")" }); f != "" {
			b.WriteString("\n" + f + "\n")
		}
		return b.String()
	}
	return fitRows(t, build, fit)
}

var mdCell = strings.NewReplacer("|", `\|`, "\r\n", " ", "\n", " ", "\r", " ", "`", "\\`")

func mdRow(cells []string) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		parts[i] = mdCell.Replace(c)
	}
	return "| " + strings.Join(parts, " | ") + " |"
}

func mdEscape(s string) string { return strings.NewReplacer("[", `\[`, "]", `\]`).Replace(s) }

// wide says whether a Markdown table would be wider than a chat message reads well.
func wide(t *table) bool {
	lines := append([][]string{t.names}, t.rows...)
	for _, l := range lines {
		if utf8.RuneCountInString(mdRow(l)) > wideMarkup {
			return true
		}
	}
	return false
}

// renderText writes Telegram's HTML parse mode: the table as preformatted text, everything
// escaped.
func renderText(t *table, fit func(string) bool) (string, int) {
	build := func(int) string {
		var b strings.Builder
		if len(t.names) > 0 {
			b.WriteString("<pre>")
			b.WriteString(html.EscapeString(strings.Join(grid(t), "\n")))
			b.WriteString("</pre>\n")
		}
		if f := t.footer(func(text, href string) string {
			return `<a href="` + html.EscapeString(href) + `">` + html.EscapeString(text) + "</a>"
		}); f != "" {
			b.WriteString(footerHTML(f) + "\n")
		}
		return b.String()
	}
	return fitRows(t, build, fit)
}
