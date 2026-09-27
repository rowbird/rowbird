// Package params finds {{name}} parameters in SQL, resolves their values (built-in dates and
// user-defined parameters) and binds them as driver placeholders. Values are never interpolated
// into the SQL text (AGENTS.md, hard rules; docs/spec/03-flows.md, "Parameters").
package params

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/sqlscan"
)

// Type is the type of a parameter.
type Type string

// Parameter types.
const (
	Text     Type = "text"
	Integer  Type = "integer"
	Decimal  Type = "decimal"
	Boolean  Type = "boolean"
	Date     Type = "date"
	DateTime Type = "datetime"
)

// Types lists every parameter type.
var Types = []Type{Text, Integer, Decimal, Boolean, Date, DateTime}

// Definition is a user-defined parameter of a query version. Default is text in the same format
// the API accepts for values.
type Definition struct {
	Name    string  `json:"name"`
	Type    Type    `json:"type"`
	Default *string `json:"default,omitempty"`
}

// Built-in parameters, resolved in a time zone (docs/spec/03-flows.md).
var builtins = map[string]Type{
	"now": DateTime, "today": Date, "yesterday": Date, "start_of_week": Date, "start_of_month": Date,
	"start_of_last_month": Date, "end_of_last_month": Date, "start_of_year": Date,
}

// Builtins returns the built-in parameter names, sorted.
func Builtins() []string {
	out := make([]string, 0, len(builtins))
	for n := range builtins {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// IsBuiltin reports whether name is a built-in parameter.
func IsBuiltin(name string) bool { _, ok := builtins[name]; return ok }

// Occurrence is one {{name}} in the SQL, with byte offsets of the whole placeholder.
type Occurrence struct {
	Name       string
	Start, End int
}

var (
	placeholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
	nameRe        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
)

// Find returns the parameters in code, ignoring string literals, quoted identifiers and comments.
func Find(d sqlscan.Dialect, sql string) []Occurrence {
	var out []Occurrence
	for _, tok := range sqlscan.Tokenize(d, sql) {
		if tok.Kind != sqlscan.Code {
			continue
		}
		for _, m := range placeholderRe.FindAllStringSubmatchIndex(tok.Text, -1) {
			out = append(out, Occurrence{Name: tok.Text[m[2]:m[3]], Start: tok.Start + m[0], End: tok.Start + m[1]})
		}
	}
	return out
}

// Names returns the distinct parameter names in order of first appearance.
func Names(d sqlscan.Dialect, sql string) []string {
	var out []string
	for _, o := range Find(d, sql) {
		if !slices.Contains(out, o.Name) {
			out = append(out, o.Name)
		}
	}
	return out
}

// Validation codes.
const (
	CodeUndefined    = "validation.param_undefined"
	CodeRequired     = "validation.required"
	CodeInvalidValue = "validation.invalid_value"
	CodeInvalidName  = "validation.param_name"
	CodeDuplicate    = "validation.duplicate"
	CodeReserved     = "validation.param_reserved"
)

// ValidateDefinitions checks names, types and defaults. Field paths are "params[i].field".
func ValidateDefinitions(defs []Definition) []apperr.FieldError {
	var fe []apperr.FieldError
	seen := map[string]bool{}
	for i, d := range defs {
		at := func(f string) string { return fmt.Sprintf("params[%d].%s", i, f) }
		switch {
		case !nameRe.MatchString(d.Name):
			fe = append(fe, apperr.Field(at("name"), CodeInvalidName))
		case IsBuiltin(d.Name):
			fe = append(fe, apperr.Field(at("name"), CodeReserved))
		case seen[d.Name]:
			fe = append(fe, apperr.Field(at("name"), CodeDuplicate))
		}
		seen[d.Name] = true
		if !slices.Contains(Types, d.Type) {
			fe = append(fe, apperr.Field(at("type"), CodeInvalidValue))
			continue
		}
		if d.Default != nil {
			if _, _, err := parse(d.Type, *d.Default, time.UTC); err != nil {
				fe = append(fe, apperr.Field(at("default"), CodeInvalidValue))
			}
		}
	}
	return fe
}

// CheckUsage reports parameters used in sql that are neither built-in nor defined.
func CheckUsage(d sqlscan.Dialect, sql string, defs []Definition) []apperr.FieldError {
	var fe []apperr.FieldError
	for _, n := range Names(d, sql) {
		if IsBuiltin(n) || slices.ContainsFunc(defs, func(def Definition) bool { return def.Name == n }) {
			continue
		}
		fe = append(fe, apperr.Field("sql."+n, CodeUndefined))
	}
	return fe
}

// Resolved is a parameter with its value. Value is what the driver receives (see Bind); Display
// is how the value is shown to people.
type Resolved struct {
	Name    string `json:"name"`
	Type    Type   `json:"type"`
	Display string `json:"value"`
	Builtin bool   `json:"builtin"`
	value   any
}

// Resolve computes the value of every name: built-ins from now in loc, user parameters from
// values, falling back to their default. Values are text in the API format.
func Resolve(names []string, defs []Definition, values map[string]string, now time.Time, loc *time.Location) ([]Resolved, error) {
	var fe []apperr.FieldError
	out := make([]Resolved, 0, len(names))
	for _, n := range names {
		if t, ok := builtins[n]; ok {
			v, display := builtin(n, now.In(loc))
			out = append(out, Resolved{Name: n, Type: t, Display: display, Builtin: true, value: v})
			continue
		}
		i := slices.IndexFunc(defs, func(d Definition) bool { return d.Name == n })
		if i < 0 {
			fe = append(fe, apperr.Field("values."+n, CodeUndefined))
			continue
		}
		def := defs[i]
		raw, ok := values[n]
		if !ok && def.Default != nil {
			raw, ok = *def.Default, true
		}
		if !ok {
			fe = append(fe, apperr.Field("values."+n, CodeRequired))
			continue
		}
		v, display, err := parse(def.Type, raw, loc)
		if err != nil {
			fe = append(fe, apperr.Field("values."+n, CodeInvalidValue))
			continue
		}
		out = append(out, Resolved{Name: n, Type: def.Type, Display: display, value: v})
	}
	if len(fe) > 0 {
		return nil, apperr.Invalid(fe...)
	}
	return out, nil
}

// builtin computes a built-in from the local time now. Date arithmetic uses calendar fields, so
// daylight saving changes cannot shift a day.
func builtin(name string, now time.Time) (any, string) {
	y, m, d := now.Date()
	date := func(y int, m time.Month, d int) (any, string) {
		t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC) // normalizes overflow, zone irrelevant
		s := t.Format(time.DateOnly)
		return civilDate(s), s
	}
	switch name {
	case "now":
		return now, now.Format(time.RFC3339)
	case "today":
		return date(y, m, d)
	case "yesterday":
		return date(y, m, d-1)
	case "start_of_week":
		return date(y, m, d-(int(now.Weekday())+6)%7) // Monday (ISO 8601)
	case "start_of_month":
		return date(y, m, 1)
	case "start_of_last_month":
		return date(y, m-1, 1)
	case "end_of_last_month":
		return date(y, m, 0)
	case "start_of_year":
		return date(y, time.January, 1)
	}
	return nil, ""
}

// civilDate is a calendar date, "2006-01-02".
type civilDate string

var decimalRe = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)$`)

// parse reads a value in the API format for type t.
func parse(t Type, raw string, loc *time.Location) (any, string, error) {
	s := strings.TrimSpace(raw)
	switch t {
	case Text:
		if utf8.RuneCountInString(raw) > 10000 {
			return nil, "", fmt.Errorf("too long")
		}
		return raw, raw, nil
	case Integer:
		n, err := strconv.ParseInt(s, 10, 64)
		return n, s, err
	case Decimal:
		if !decimalRe.MatchString(s) {
			return nil, "", fmt.Errorf("not a decimal")
		}
		return plugin.Decimal(s), s, nil
	case Boolean:
		b, err := strconv.ParseBool(s)
		return b, strconv.FormatBool(b), err
	case Date:
		d, err := time.Parse(time.DateOnly, s)
		if err != nil {
			return nil, "", err
		}
		return civilDate(d.Format(time.DateOnly)), d.Format(time.DateOnly), nil
	case DateTime:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
			var dt time.Time
			var err error
			if layout == time.RFC3339Nano {
				dt, err = time.Parse(layout, s)
			} else {
				dt, err = time.ParseInLocation(layout, s, loc)
			}
			if err == nil {
				dt = dt.In(loc)
				return dt, dt.Format(time.RFC3339), nil
			}
		}
		return nil, "", fmt.Errorf("not a date and time")
	}
	return nil, "", fmt.Errorf("unknown type %q", t)
}

// driverValue converts a resolved value to what the driver receives. Dates travel as text that the
// database converts using the column's type; date-times as local wall time, with the offset only
// for PostgreSQL (so timestamptz compares the instant and timestamp the wall time).
func driverValue(d sqlscan.Dialect, r Resolved) any {
	switch v := r.value.(type) {
	case civilDate:
		return string(v)
	case time.Time:
		if d == sqlscan.Postgres {
			return v.Format("2006-01-02 15:04:05.999999-07:00")
		}
		return v.Format("2006-01-02 15:04:05")
	case plugin.Decimal:
		return string(v)
	}
	return r.value
}

// Bind replaces every {{name}} with the placeholder of style and returns the arguments in order.
func Bind(d sqlscan.Dialect, style, sql string, resolved []Resolved) (plugin.BoundQuery, error) {
	occ := Find(d, sql)
	byName := map[string]Resolved{}
	for _, r := range resolved {
		byName[r.Name] = r
	}
	var b strings.Builder
	var args []any
	index := map[string]int{}
	last := 0
	for _, o := range occ {
		r, ok := byName[o.Name]
		if !ok {
			return plugin.BoundQuery{}, apperr.Invalid(apperr.Field("values."+o.Name, CodeUndefined))
		}
		b.WriteString(sql[last:o.Start])
		last = o.End
		switch style {
		case "question":
			b.WriteString("?")
			args = append(args, driverValue(d, r))
		case "dollar", "at":
			n, seen := index[o.Name]
			if !seen {
				args = append(args, driverValue(d, r))
				n = len(args)
				index[o.Name] = n
			}
			if style == "dollar" {
				fmt.Fprintf(&b, "$%d", n)
			} else {
				fmt.Fprintf(&b, "@p%d", n)
			}
		default:
			return plugin.BoundQuery{}, fmt.Errorf("params: unknown placeholder style %q", style)
		}
	}
	b.WriteString(sql[last:])
	return plugin.BoundQuery{SQL: b.String(), Args: args}, nil
}
