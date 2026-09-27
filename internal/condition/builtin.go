package condition

import (
	"context"
	"embed"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/rowbird/rowbird/internal/plugin"
)

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MustLoadMessages(locales)

// Built-in condition ids.
const (
	IDAlways   = "always"
	IDHasRows  = "has_rows"
	IDIsEmpty  = "is_empty"
	IDRowCount = "row_count"
	IDValue    = "value"
	IDChanged  = "changed"
)

// Comparison operators.
const (
	OpEq      = "eq"
	OpNe      = "ne"
	OpGt      = "gt"
	OpGte     = "gte"
	OpLt      = "lt"
	OpLte     = "lte"
	OpBetween = "between"
)

var countOps = []string{OpEq, OpNe, OpGt, OpGte, OpLt, OpLte}

func init() {
	for _, c := range []plugin.Condition{
		simple{id: IDAlways, icon: "check", eval: func(plugin.ConditionInput) (bool, map[string]any) { return true, nil }},
		simple{id: IDHasRows, icon: "rows", eval: func(in plugin.ConditionInput) (bool, map[string]any) {
			return in.RowCount > 0, map[string]any{"row_count": in.RowCount}
		}},
		simple{id: IDIsEmpty, icon: "empty", eval: func(in plugin.ConditionInput) (bool, map[string]any) {
			return in.RowCount == 0, map[string]any{"row_count": in.RowCount}
		}},
		simple{id: IDChanged, icon: "diff", eval: func(in plugin.ConditionInput) (bool, map[string]any) {
			if in.PreviousHash == "" {
				return true, map[string]any{"first_result": true}
			}
			return in.ResultHash != in.PreviousHash, nil
		}},
		rowCount{},
		value{},
	} {
		plugin.Register(plugin.KindCondition, c.Meta().ID, func() plugin.Plugin { return c })
	}
}

func meta(id, icon string) plugin.Metadata {
	return plugin.Metadata{
		ID: id, Name: "plugin.condition." + id + ".name", Description: "plugin.condition." + id + ".description",
		Icon: icon, Version: "1.0.0",
	}
}

// simple is a condition without parameters.
type simple struct {
	id, icon string
	eval     func(plugin.ConditionInput) (bool, map[string]any)
}

func (c simple) Meta() plugin.Metadata      { return meta(c.id, c.icon) }
func (simple) ConfigSchema() *plugin.Schema { return &plugin.Schema{} }
func (simple) Capabilities() any            { return plugin.ConditionCapabilities{} }
func (simple) Messages() plugin.Messages    { return messages }
func (c simple) Evaluate(_ context.Context, in plugin.ConditionInput, _ map[string]any) (plugin.RuleOutcome, error) {
	ok, detail := c.eval(in)
	return plugin.RuleOutcome{Passed: ok, Detail: detail}, nil
}

// rowCount compares the number of rows. When the row limit truncated the result the count is a
// lower bound, which the detail reports.
type rowCount struct{}

func (rowCount) Meta() plugin.Metadata { return meta(IDRowCount, "hash") }
func (rowCount) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "op", Type: plugin.TypeString, Required: true, Default: OpGt, Enum: countOps, Label: "plugin.condition.row_count.op.label"},
		{Key: "value", Type: plugin.TypeInteger, Required: true, Default: int64(0), Minimum: plugin.Float(0), Label: "plugin.condition.row_count.value.label"},
	}}
}
func (rowCount) Capabilities() any         { return plugin.ConditionCapabilities{} }
func (rowCount) Messages() plugin.Messages { return messages }
func (rowCount) Evaluate(_ context.Context, in plugin.ConditionInput, p map[string]any) (plugin.RuleOutcome, error) {
	op, _ := p["op"].(string)
	want, _ := p["value"].(int64)
	cmp := 0
	switch {
	case in.RowCount < want:
		cmp = -1
	case in.RowCount > want:
		cmp = 1
	}
	detail := map[string]any{"row_count": in.RowCount}
	if in.Truncated {
		detail["truncated"] = true
	}
	return plugin.RuleOutcome{Passed: holds(op, cmp), Detail: detail}, nil
}

// value compares a column of the first row with a value, using the column's type: exact numbers
// for integers and decimals, calendar order for dates and times, byte order for text. NULL and an
// empty result never pass.
type value struct{}

func (value) Meta() plugin.Metadata { return meta(IDValue, "equal") }
func (value) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "column", Type: plugin.TypeString, Required: true, MaxLength: 255, Label: "plugin.condition.value.column.label"},
		{Key: "op", Type: plugin.TypeString, Required: true, Default: OpEq, Enum: append(append([]string{}, countOps...), OpBetween), Label: "plugin.condition.value.op.label"},
		{Key: "value", Type: plugin.TypeString, Required: true, MaxLength: 1000, Label: "plugin.condition.value.value.label", Help: "plugin.condition.value.value.help"},
		{Key: "value2", Type: plugin.TypeString, Required: true, MaxLength: 1000, Label: "plugin.condition.value.value2.label", ShowIf: &plugin.ShowIf{Field: "op", In: []any{OpBetween}}},
	}}
}
func (value) Capabilities() any         { return plugin.ConditionCapabilities{NeedsColumn: true} }
func (value) Messages() plugin.Messages { return messages }
func (value) Evaluate(_ context.Context, in plugin.ConditionInput, p map[string]any) (plugin.RuleOutcome, error) {
	name, _ := p["column"].(string)
	op, _ := p["op"].(string)
	idx := -1
	for i, c := range in.Columns {
		if c.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return plugin.RuleOutcome{}, &plugin.ConditionError{Code: plugin.ErrCodeConditionColumn, Detail: map[string]any{"column": name}}
	}
	col := in.Columns[idx]
	loc := in.Location
	if loc == nil {
		loc = time.UTC
	}
	// Parse the expected values first, so a bad value fails even on an empty result.
	lo, err := parseExpected(col, p["value"], loc)
	if err != nil {
		return plugin.RuleOutcome{}, err
	}
	var hi any
	if op == OpBetween {
		if hi, err = parseExpected(col, p["value2"], loc); err != nil {
			return plugin.RuleOutcome{}, err
		}
	}
	if col.Type == plugin.TypeBool && op != OpEq && op != OpNe {
		return plugin.RuleOutcome{}, invalidValue(col, p["value"])
	}
	if in.FirstRow == nil {
		return plugin.RuleOutcome{Passed: false, Detail: map[string]any{"no_rows": true}}, nil
	}
	actual := in.FirstRow[idx]
	detail := map[string]any{"actual": display(actual)}
	if actual == nil {
		return plugin.RuleOutcome{Passed: false, Detail: detail}, nil
	}
	got, err := normalize(col, actual)
	if err != nil {
		return plugin.RuleOutcome{}, err
	}
	if op == OpBetween {
		return plugin.RuleOutcome{Passed: compare(got, lo) >= 0 && compare(got, hi) <= 0, Detail: detail}, nil
	}
	return plugin.RuleOutcome{Passed: holds(op, compare(got, lo)), Detail: detail}, nil
}

func holds(op string, cmp int) bool {
	switch op {
	case OpEq:
		return cmp == 0
	case OpNe:
		return cmp != 0
	case OpGt:
		return cmp > 0
	case OpGte:
		return cmp >= 0
	case OpLt:
		return cmp < 0
	case OpLte:
		return cmp <= 0
	}
	return false
}

func invalidValue(col plugin.Column, v any) error {
	return &plugin.ConditionError{Code: plugin.ErrCodeConditionValue, Detail: map[string]any{"column": col.Name, "type": string(col.Type), "value": v}}
}

// Comparable forms: *big.Rat (integer, decimal), float64, bool, time.Time (datetime) and string
// (text, date, time).
func parseExpected(col plugin.Column, raw any, loc *time.Location) (any, error) {
	s, _ := raw.(string)
	s = strings.TrimSpace(s)
	switch col.Type {
	case plugin.TypeInt, plugin.TypeDecimal:
		if r, ok := new(big.Rat).SetString(s); ok {
			return r, nil
		}
	case plugin.TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, nil
		}
	case plugin.TypeBool:
		if b, err := strconv.ParseBool(s); err == nil {
			return b, nil
		}
	case plugin.TypeDate:
		if _, err := time.Parse(time.DateOnly, s); err == nil {
			return s, nil
		}
	case plugin.TypeTime:
		for _, layout := range []string{time.TimeOnly, "15:04"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.Format(time.TimeOnly), nil
			}
		}
	case plugin.TypeDateTime:
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t, nil
		}
		// A value without offset is local time: in the report's zone for columns with a time zone,
		// and plain wall time (read as UTC, like the driver does) for columns without one.
		wall := time.UTC
		if col.WithTimeZone {
			wall = loc
		}
		for _, layout := range []string{time.DateTime, "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02T15:04", time.DateOnly} {
			if t, err := time.ParseInLocation(layout, s, wall); err == nil {
				return t, nil
			}
		}
	case plugin.TypeText, plugin.TypeJSON, plugin.TypeUnknown:
		return s, nil
	}
	return nil, invalidValue(col, raw)
}

func normalize(col plugin.Column, v any) (any, error) {
	switch x := v.(type) {
	case int64:
		return new(big.Rat).SetInt64(x), nil
	case plugin.Decimal:
		if r, ok := new(big.Rat).SetString(string(x)); ok {
			return r, nil
		}
	case float64:
		return x, nil
	case bool:
		return x, nil
	case time.Time:
		return x, nil
	case plugin.Date:
		return string(x), nil
	case plugin.TimeOfDay:
		return string(x), nil
	case plugin.JSON:
		return string(x), nil
	case string:
		return x, nil
	}
	return nil, invalidValue(col, display(v))
}

// compare orders two values of the same comparable form.
func compare(a, b any) int {
	switch x := a.(type) {
	case *big.Rat:
		if y, ok := b.(*big.Rat); ok {
			return x.Cmp(y)
		}
	case float64:
		if y, ok := b.(float64); ok {
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			}
			return 0
		}
	case bool:
		if y, ok := b.(bool); ok && x == y {
			return 0
		}
		return 1
	case time.Time:
		if y, ok := b.(time.Time); ok {
			return x.Compare(y)
		}
	case string:
		if y, ok := b.(string); ok {
			return strings.Compare(x, y)
		}
	}
	return 1
}

// display renders an actual value for the run detail.
func display(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case []byte:
		return fmt.Sprintf("(%d bytes)", len(x))
	case int64, float64, bool:
		return x
	}
	return fmt.Sprint(v)
}
