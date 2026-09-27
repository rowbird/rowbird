package params

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/sqlscan"
)

func ptr(s string) *string { return &s }

func TestFindIgnoresLiteralsAndComments(t *testing.T) {
	cases := []struct {
		d    sqlscan.Dialect
		sql  string
		want []string
	}{
		{sqlscan.Postgres, "select * from t where d >= {{today}} and x = {{ region }}", []string{"today", "region"}},
		{sqlscan.Postgres, "select '{{not}}', \"{{nor}}\" -- {{this}}\n/* {{or_this}} */ , {{yes}}", []string{"yes"}},
		{sqlscan.Postgres, "select $$ {{inside}} $$, {{a}}, {{a}}", []string{"a"}},
		{sqlscan.MSSQL, "select [{{col}}], {{p}} from t", []string{"p"}},
		{sqlscan.MySQL, "select `{{col}}`, \"{{str}}\", {{p}} # {{c}}\n", []string{"p"}},
		{sqlscan.Postgres, "select {{1bad}}, {{ok_1}}, {{ spaced }}", []string{"ok_1", "spaced"}},
		{sqlscan.Postgres, "select '{a,b}'::text[], jsonb '{\"k\": 1}'", nil},
	}
	for _, tc := range cases {
		if got := Names(tc.d, tc.sql); !slices.Equal(got, tc.want) {
			t.Errorf("%s %q: got %v, want %v", tc.d, tc.sql, got, tc.want)
		}
	}
}

func resolveAll(t *testing.T, names []string, defs []Definition, values map[string]string, now time.Time, loc *time.Location) map[string]Resolved {
	t.Helper()
	rs, err := Resolve(names, defs, values, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Resolved{}
	for _, r := range rs {
		out[r.Name] = r
	}
	return out
}

func TestBuiltins(t *testing.T) {
	sp, _ := time.LoadLocation("America/Sao_Paulo")
	cases := []struct {
		name string
		now  time.Time
		loc  *time.Location
		want map[string]string
	}{
		{
			"ordinary Thursday", time.Date(2026, 9, 24, 15, 0, 0, 0, time.UTC), time.UTC,
			map[string]string{
				"today": "2026-09-24", "yesterday": "2026-09-23", "start_of_week": "2026-09-21",
				"start_of_month": "2026-09-01", "start_of_last_month": "2026-08-01", "end_of_last_month": "2026-08-31",
				"start_of_year": "2026-01-01", "now": "2026-09-24T15:00:00Z",
			},
		},
		{
			// 01:30 UTC on Sept 1st is still Aug 31st in São Paulo.
			"time zone decides the day", time.Date(2026, 9, 1, 1, 30, 0, 0, time.UTC), sp,
			map[string]string{"today": "2026-08-31", "start_of_month": "2026-08-01", "end_of_last_month": "2026-07-31", "now": "2026-08-31T22:30:00-03:00"},
		},
		{
			"Monday is the first day of the week", time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC), time.UTC,
			map[string]string{"start_of_week": "2026-09-21"},
		},
		{
			"Sunday belongs to the week that started on Monday", time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC), time.UTC,
			map[string]string{"start_of_week": "2026-09-21", "yesterday": "2026-09-26"},
		},
		{
			"January wraps to December", time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC), time.UTC,
			map[string]string{"yesterday": "2026-12-31", "start_of_last_month": "2026-12-01", "end_of_last_month": "2026-12-31", "start_of_year": "2027-01-01"},
		},
		{
			"leap year February", time.Date(2028, 3, 10, 12, 0, 0, 0, time.UTC), time.UTC,
			map[string]string{"end_of_last_month": "2028-02-29"},
		},
		{
			// New York springs forward on 2026-03-08; the date math is on calendar fields.
			"daylight saving day", time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC), mustLoc(t, "America/New_York"),
			map[string]string{"today": "2026-03-08", "yesterday": "2026-03-07"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveAll(t, Builtins(), nil, nil, tc.now, tc.loc)
			for k, v := range tc.want {
				if got[k].Display != v {
					t.Errorf("%s = %s, want %s", k, got[k].Display, v)
				}
				if !got[k].Builtin {
					t.Errorf("%s not marked built-in", k)
				}
			}
		})
	}
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestUserParameters(t *testing.T) {
	defs := []Definition{
		{Name: "region", Type: Text, Default: ptr("south")},
		{Name: "min_total", Type: Decimal},
		{Name: "limit", Type: Integer, Default: ptr("10")},
		{Name: "active", Type: Boolean, Default: ptr("true")},
		{Name: "since", Type: Date},
		{Name: "at", Type: DateTime},
	}
	names := []string{"region", "min_total", "limit", "active", "since", "at"}
	loc := mustLoc(t, "America/Sao_Paulo")
	got := resolveAll(t, names, defs, map[string]string{"min_total": "1234567890123456789.50", "since": "2026-02-28", "at": "2026-09-25 08:00"}, time.Now(), loc)
	checks := map[string]string{"region": "south", "min_total": "1234567890123456789.50", "limit": "10", "active": "true", "since": "2026-02-28", "at": "2026-09-25T08:00:00-03:00"}
	for k, v := range checks {
		if got[k].Display != v {
			t.Errorf("%s = %q, want %q", k, got[k].Display, v)
		}
	}
	if got["min_total"].value != plugin.Decimal("1234567890123456789.50") {
		t.Error("decimal must stay exact text")
	}

	_, err := Resolve(names, defs, map[string]string{"min_total": "12,5", "since": "2026-02-30"}, time.Now(), loc)
	fields := fieldCodes(t, err)
	if fields["values.min_total"] != CodeInvalidValue || fields["values.since"] != CodeInvalidValue || fields["values.at"] != CodeRequired {
		t.Fatalf("fields %v", fields)
	}
	_, err = Resolve([]string{"ghost"}, defs, nil, time.Now(), loc)
	if fieldCodes(t, err)["values.ghost"] != CodeUndefined {
		t.Fatal("undefined parameter not reported")
	}
}

func fieldCodes(t *testing.T, err error) map[string]string {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("got %v, want a validation error", err)
	}
	out := map[string]string{}
	for _, f := range e.Fields {
		out[f.Field] = f.Code
	}
	return out
}

func TestValidateDefinitionsAndUsage(t *testing.T) {
	fe := ValidateDefinitions([]Definition{
		{Name: "ok", Type: Text},
		{Name: "ok", Type: Text},
		{Name: "today", Type: Date},
		{Name: "2bad", Type: Text},
		{Name: "n", Type: "money"},
		{Name: "d", Type: Date, Default: ptr("25/09/2026")},
	})
	got := map[string]string{}
	for _, f := range fe {
		got[f.Field] = f.Code
	}
	want := map[string]string{"params[1].name": CodeDuplicate, "params[2].name": CodeReserved, "params[3].name": CodeInvalidName, "params[4].type": CodeInvalidValue, "params[5].default": CodeInvalidValue}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	usage := CheckUsage(sqlscan.Postgres, "select {{today}}, {{region}}, {{missing}}", []Definition{{Name: "region", Type: Text}})
	if len(usage) != 1 || usage[0].Field != "sql.missing" || usage[0].Code != CodeUndefined {
		t.Fatalf("usage %v", usage)
	}
}

func TestBind(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 11, 12, 0, time.UTC)
	loc := mustLoc(t, "America/Sao_Paulo")
	defs := []Definition{{Name: "region", Type: Text}, {Name: "n", Type: Integer}}
	values := map[string]string{"region": "o'reilly'; drop table x; --", "n": "7"}
	sql := "select * from t where r = {{region}} and d >= {{today}} and r <> {{ region }} and n = {{n}} and at < {{now}}"
	rs, err := Resolve(Names(sqlscan.Postgres, sql), defs, values, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		d     sqlscan.Dialect
		style string
		sql   string
		args  []any
	}{
		{
			sqlscan.Postgres, "dollar", "select * from t where r = $1 and d >= $2 and r <> $1 and n = $3 and at < $4",
			[]any{values["region"], "2026-09-25", int64(7), "2026-09-25 07:11:12-03:00"},
		},
		{
			sqlscan.MySQL, "question", "select * from t where r = ? and d >= ? and r <> ? and n = ? and at < ?",
			[]any{values["region"], "2026-09-25", values["region"], int64(7), "2026-09-25 07:11:12"},
		},
		{
			sqlscan.MSSQL, "at", "select * from t where r = @p1 and d >= @p2 and r <> @p1 and n = @p3 and at < @p4",
			[]any{values["region"], "2026-09-25", int64(7), "2026-09-25 07:11:12"},
		},
	}
	for _, tc := range cases {
		q, err := Bind(tc.d, tc.style, sql, rs)
		if err != nil {
			t.Fatal(err)
		}
		if q.SQL != tc.sql {
			t.Errorf("%s: sql %q", tc.style, q.SQL)
		}
		if len(q.Args) != len(tc.args) {
			t.Fatalf("%s: args %v", tc.style, q.Args)
		}
		for i := range tc.args {
			if q.Args[i] != tc.args[i] {
				t.Errorf("%s arg %d: %#v, want %#v", tc.style, i, q.Args[i], tc.args[i])
			}
		}
		if strings.Contains(q.SQL, "o'reilly") {
			t.Fatal("value interpolated into SQL")
		}
	}

	// Literals keep their text untouched.
	q, _ := Bind(sqlscan.Postgres, "dollar", "select '{{today}}', {{today}}", rs)
	if q.SQL != "select '{{today}}', $1" {
		t.Fatalf("got %q", q.SQL)
	}
	if _, err := Bind(sqlscan.Postgres, "dollar", "select {{ghost}}", rs); err == nil {
		t.Fatal("unresolved parameter bound")
	}
	if _, err := Bind(sqlscan.Postgres, "colon", "select {{n}}", rs); err == nil {
		t.Fatal("unknown style accepted")
	}
}
