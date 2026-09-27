// Package format holds what every formatter shares: reading values the way a person expects in
// their language, neutralizing spreadsheet formulas, the texts that outputs contain and helpers to
// iterate rows. Formatter plugins live in its subpackages (docs/spec/04-plugins.md).
package format

import (
	"embed"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rowbird/rowbird/internal/plugin"
)

// Locale describes how numbers and dates read in a language.
type Locale struct {
	Tag     string
	Decimal string
	Group   string
	// Go layouts for dates and date-times (with and without seconds).
	Date, DateTime, DateTimeSeconds string
}

var locales = map[string]Locale{
	"en":    {Tag: "en", Decimal: ".", Group: ",", Date: "2006-01-02", DateTime: "2006-01-02 15:04", DateTimeSeconds: "2006-01-02 15:04:05"},
	"pt-BR": {Tag: "pt-BR", Decimal: ",", Group: ".", Date: "02/01/2006", DateTime: "02/01/2006 15:04", DateTimeSeconds: "02/01/2006 15:04:05"},
}

// GetLocale returns a supported locale, English for anything else.
func GetLocale(tag string) Locale {
	if l, ok := locales[tag]; ok {
		return l
	}
	return locales["en"]
}

// Number rewrites a plain decimal string ("-1234.5") with the locale's separators, grouping the
// integer part when group is true. It never goes through float64, so every digit is kept. Strings
// that are not plain decimals (exponents, NaN) are returned unchanged.
func (l Locale) Number(s string, group bool) string {
	return formatNumber(s, l.Decimal, l.Group, group)
}

func formatNumber(s, decimal, groupSep string, group bool) string {
	sign := ""
	body := s
	if strings.HasPrefix(body, "-") || strings.HasPrefix(body, "+") {
		if body[0] == '-' {
			sign = "-"
		}
		body = body[1:]
	}
	intPart, frac, hasFrac := strings.Cut(body, ".")
	if intPart == "" && hasFrac {
		intPart = "0"
	}
	if intPart == "" || !digits(intPart) || (hasFrac && (frac == "" || !digits(frac))) {
		return s
	}
	if group && len(intPart) > 3 {
		var b strings.Builder
		lead := len(intPart) % 3
		if lead > 0 {
			b.WriteString(intPart[:lead])
		}
		for i := lead; i < len(intPart); i += 3 {
			if b.Len() > 0 {
				b.WriteString(groupSep)
			}
			b.WriteString(intPart[i : i+3])
		}
		intPart = b.String()
	}
	if hasFrac {
		return sign + intPart + decimal + frac
	}
	return sign + intPart
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// NumberText is the plain decimal text of a numeric value (int64, float64 or Decimal), and false
// for other values.
func NumberText(v any) (string, bool) {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case plugin.Decimal:
		return string(x), true
	}
	return "", false
}

// Instant returns a datetime as it should be shown: in the report's time zone when the column has
// one, as the stored wall time otherwise.
func Instant(col plugin.Column, t time.Time, loc *time.Location) time.Time {
	if col.WithTimeZone && loc != nil {
		return t.In(loc)
	}
	return t
}

// FormatDateTime formats a datetime, with seconds only when they are not zero.
func (l Locale) FormatDateTime(t time.Time) string {
	if t.Second() != 0 || t.Nanosecond() != 0 {
		return t.Format(l.DateTimeSeconds)
	}
	return t.Format(l.DateTime)
}

// FormatDate formats a "YYYY-MM-DD" date; anything else is returned unchanged.
func (l Locale) FormatDate(d string) string {
	t, err := time.Parse(time.DateOnly, d)
	if err != nil {
		return d
	}
	return t.Format(l.Date)
}

// Text renders any value for people to read (PDF, HTML, messages): numbers grouped with the
// locale's separators, dates in the locale's order, datetimes in the report's time zone, booleans
// as yes/no. NULL is the empty string.
func Text(col plugin.Column, v any, locale string, loc *time.Location) string {
	l := GetLocale(locale)
	switch x := v.(type) {
	case nil:
		return ""
	case int64, float64, plugin.Decimal:
		n, _ := NumberText(x)
		return l.Number(n, true)
	case bool:
		if x {
			return T(locale, "format.yes", nil)
		}
		return T(locale, "format.no", nil)
	case plugin.Date:
		return l.FormatDate(string(x))
	case time.Time:
		return l.FormatDateTime(Instant(col, x, loc))
	case []byte:
		return T(locale, "format.bytes", map[string]any{"n": len(x)})
	case plugin.TimeOfDay:
		return string(x)
	case plugin.JSON:
		return string(x)
	case string:
		return x
	}
	return fmt.Sprint(v)
}

// Neutralize defuses spreadsheet formulas: text that starts with = + - @, a tab or a carriage
// return gets a leading apostrophe (docs/spec/07-security.md, "Generated content").
func Neutralize(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

//go:embed locales/*.json
var catalogFS embed.FS

var catalog = plugin.MustLoadMessages(catalogFS)

// Messages returns the output texts, for plugins that expose them.
func Messages() plugin.Messages { return catalog }

// T returns an output text in locale (English when missing) with {name} placeholders replaced.
func T(locale, key string, args map[string]any) string {
	s, ok := catalog[locale][key]
	if !ok {
		s, ok = catalog["en"][key]
	}
	if !ok {
		return key
	}
	for k, v := range args {
		s = strings.ReplaceAll(s, "{"+k+"}", fmt.Sprint(v))
	}
	return s
}

// Rows adapts a slice to plugin.RowIterator, for tests and small results.
type Rows struct {
	data [][]any
	i    int
}

// SliceRows iterates over rows.
func SliceRows(rows [][]any) *Rows { return &Rows{data: rows} }

// Next implements plugin.RowIterator.
func (r *Rows) Next() bool { r.i++; return r.i <= len(r.data) }

// Row implements plugin.RowIterator.
func (r *Rows) Row() []any { return r.data[r.i-1] }

// Err implements plugin.RowIterator.
func (r *Rows) Err() error { return nil }

// String returns a string option, or def.
func String(opts map[string]any, key, def string) string {
	if s, ok := opts[key].(string); ok && s != "" {
		return s
	}
	return def
}

// Bool returns a boolean option, or def.
func Bool(opts map[string]any, key string, def bool) bool {
	if b, ok := opts[key].(bool); ok {
		return b
	}
	return def
}
