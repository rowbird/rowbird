package condition

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/conditiontest"
)

var (
	cols = []plugin.Column{
		{Name: "n", Type: plugin.TypeInt},
		{Name: "total", Type: plugin.TypeDecimal},
		{Name: "ratio", Type: plugin.TypeFloat},
		{Name: "ok", Type: plugin.TypeBool},
		{Name: "day", Type: plugin.TypeDate},
		{Name: "at", Type: plugin.TypeDateTime, WithTimeZone: true},
		{Name: "naive", Type: plugin.TypeDateTime},
		{Name: "clock", Type: plugin.TypeTime},
		{Name: "name", Type: plugin.TypeText},
		{Name: "blob", Type: plugin.TypeBinary},
		{Name: "empty", Type: plugin.TypeText},
	}
	sp, _ = time.LoadLocation("America/Sao_Paulo")
	row   = []any{
		int64(42), plugin.Decimal("12345678901234567890.25"), 0.5, true, plugin.Date("2026-09-25"),
		time.Date(2026, 9, 25, 13, 0, 0, 0, time.UTC), time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC),
		plugin.TimeOfDay("08:30:00"), "Ana",
		[]byte{1, 2},
		nil,
	}
	result = plugin.ConditionInput{Columns: cols, FirstRow: row, RowCount: 3, ResultHash: "h2", PreviousHash: "h1", Location: sp}
	empty  = plugin.ConditionInput{Columns: cols, RowCount: 0, ResultHash: "h0", Location: sp}
)

func cond(t *testing.T, id string) plugin.Condition {
	t.Helper()
	c, ok := get(id)
	if !ok {
		t.Fatalf("condition %s is not registered", id)
	}
	return c
}

func v(column, op, value string) map[string]any {
	return map[string]any{"column": column, "op": op, "value": value}
}

func TestConformance(t *testing.T) {
	suites := map[string][]conditiontest.Case{
		IDAlways: {
			{Name: "rows", Input: result, Want: true},
			{Name: "empty", Input: empty, Want: true},
		},
		IDHasRows: {
			{Name: "rows", Input: result, Want: true},
			{Name: "empty", Input: empty, Want: false},
		},
		IDIsEmpty: {
			{Name: "rows", Input: result, Want: false},
			{Name: "empty", Input: empty, Want: true},
		},
		IDChanged: {
			{Name: "different hash", Input: result, Want: true},
			{Name: "same hash", Input: plugin.ConditionInput{ResultHash: "h", PreviousHash: "h"}, Want: false},
			{Name: "first result", Input: plugin.ConditionInput{ResultHash: "h"}, Want: true},
		},
		IDRowCount: {
			{Name: "default is more than zero", Params: map[string]any{}, Input: result, Want: true},
			{Name: "default on empty", Params: map[string]any{}, Input: empty, Want: false},
			{Name: "eq", Params: map[string]any{"op": "eq", "value": 3}, Input: result, Want: true},
			{Name: "lt", Params: map[string]any{"op": "lt", "value": 3}, Input: result, Want: false},
			{Name: "lte", Params: map[string]any{"op": "lte", "value": 3}, Input: result, Want: true},
			{Name: "ne", Params: map[string]any{"op": "ne", "value": 0}, Input: empty, Want: false},
		},
		IDValue: {
			{Name: "integer gt", Params: v("n", "gt", "41.5"), Input: result, Want: true},
			{Name: "integer eq", Params: v("n", "eq", "42"), Input: result, Want: true},
			{Name: "decimal is exact", Params: v("total", "gt", "12345678901234567890.24"), Input: result, Want: true},
			{Name: "decimal eq", Params: v("total", "eq", "12345678901234567890.250"), Input: result, Want: true},
			{Name: "float", Params: v("ratio", "lt", "0.75"), Input: result, Want: true},
			{Name: "boolean", Params: v("ok", "eq", "true"), Input: result, Want: true},
			{Name: "boolean ne", Params: v("ok", "ne", "true"), Input: result, Want: false},
			{Name: "date", Params: v("day", "gte", "2026-09-25"), Input: result, Want: true},
			{Name: "datetime in the report zone", Params: v("at", "eq", "2026-09-25 10:00"), Input: result, Want: true},
			{Name: "datetime with offset", Params: v("at", "lt", "2026-09-25T14:00:00Z"), Input: result, Want: true},
			{Name: "datetime without zone is wall time", Params: v("naive", "eq", "2026-09-25 10:00:00"), Input: result, Want: true},
			{Name: "time", Params: v("clock", "lt", "09:00"), Input: result, Want: true},
			{Name: "text", Params: v("name", "eq", "Ana"), Input: result, Want: true},
			{Name: "text order", Params: v("name", "lt", "Bruno"), Input: result, Want: true},
			{Name: "between", Params: map[string]any{"column": "n", "op": "between", "value": "40", "value2": "42"}, Input: result, Want: true},
			{Name: "between excludes", Params: map[string]any{"column": "n", "op": "between", "value": "43", "value2": "50"}, Input: result, Want: false},
			{Name: "null never passes", Params: v("empty", "ne", "x"), Input: result, Want: false},
			{Name: "empty result never passes", Params: v("n", "gt", "0"), Input: empty, Want: false},
			{Name: "missing column", Params: v("nope", "eq", "1"), Input: result, WantErr: plugin.ErrCodeConditionColumn},
			{Name: "bad number", Params: v("n", "eq", "abc"), Input: result, WantErr: plugin.ErrCodeConditionValue},
			{Name: "bad number on empty result", Params: v("n", "eq", "abc"), Input: empty, WantErr: plugin.ErrCodeConditionValue},
			{Name: "bad date", Params: v("day", "eq", "25/09/2026"), Input: result, WantErr: plugin.ErrCodeConditionValue},
			{Name: "ordering booleans", Params: v("ok", "gt", "false"), Input: result, WantErr: plugin.ErrCodeConditionValue},
			{Name: "binary", Params: v("blob", "eq", "x"), Input: result, WantErr: plugin.ErrCodeConditionValue},
		},
	}
	for id, cases := range suites {
		t.Run(id, func(t *testing.T) { conditiontest.Run(t, cond(t, id), cases) })
	}
	if got := len(plugin.List(plugin.KindCondition)); got != len(suites) {
		t.Errorf("%d conditions registered, %d tested", got, len(suites))
	}
}

func TestSpecJSON(t *testing.T) {
	raw := `{"match":"any","rules":[{"type":"row_count","op":"gt","value":10},{"type":"has_rows"}]}`
	var s Spec
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	s, err := Validate(s, "condition")
	if err != nil {
		t.Fatal(err)
	}
	want := Spec{Match: "any", Rules: []Rule{{Type: "row_count", Params: map[string]any{"op": "gt", "value": int64(10)}}, {Type: "has_rows", Params: map[string]any{}}}}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("validated = %#v", s)
	}
	b, _ := json.Marshal(s)
	var back Spec
	_ = json.Unmarshal(b, &back)
	if back, _ = Validate(back, "condition"); !reflect.DeepEqual(back, want) {
		t.Errorf("round trip = %#v", back)
	}
}

func TestValidateErrors(t *testing.T) {
	s := Spec{Match: "some", Rules: []Rule{
		{Type: "nope"},
		{Type: "row_count", Params: map[string]any{"op": "about", "value": -1}},
		{Type: "value", Params: map[string]any{"op": "between", "value": "1"}},
	}}
	_, err := Validate(s, "condition")
	ae, ok := apperr.As(err)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	got := map[string]string{}
	for _, f := range ae.Fields {
		got[f.Field] = f.Code
	}
	want := map[string]string{
		"condition.match":           plugin.CodeInvalidValue,
		"condition.rules[0].type":   plugin.CodeInvalidValue,
		"condition.rules[1].op":     plugin.CodeInvalidValue,
		"condition.rules[1].value":  plugin.CodeOutOfRange,
		"condition.rules[2].column": plugin.CodeRequired,
		"condition.rules[2].value2": plugin.CodeRequired,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %v\nwant %v", got, want)
	}
	if _, err := Validate(Spec{Rules: make([]Rule, MaxRules+1)}, "c"); err == nil {
		t.Error("too many rules accepted")
	}
}

func TestEvaluate(t *testing.T) {
	ctx := t.Context()
	hasRows, isEmpty := Rule{Type: IDHasRows}, Rule{Type: IDIsEmpty}
	cases := []struct {
		name string
		spec Spec
		want bool
		n    int
	}{
		{"no rules", Always(), true, 0},
		{"all passes", Spec{Match: MatchAll, Rules: []Rule{hasRows, {Type: IDAlways}}}, true, 2},
		{"all fails", Spec{Match: MatchAll, Rules: []Rule{hasRows, isEmpty}}, false, 2},
		{"any passes", Spec{Match: MatchAny, Rules: []Rule{isEmpty, hasRows}}, true, 2},
		{"any fails", Spec{Match: MatchAny, Rules: []Rule{isEmpty}}, false, 1},
	}
	for _, tc := range cases {
		res, err := Evaluate(ctx, tc.spec, result)
		if err != nil || res.Passed != tc.want || len(res.Rules) != tc.n {
			t.Errorf("%s: %+v %v", tc.name, res, err)
		}
	}
	_, err := Evaluate(ctx, Spec{Rules: []Rule{{Type: IDValue, Params: v("nope", "eq", "1")}}}, result)
	var ce *plugin.ConditionError
	if !errors.As(err, &ce) || ce.Code != plugin.ErrCodeConditionColumn {
		t.Errorf("err = %v", err)
	}
}
