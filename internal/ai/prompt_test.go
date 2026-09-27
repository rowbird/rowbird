package ai

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/sqlscan"
)

func TestRenderSchemaBudgetAndRelevance(t *testing.T) {
	schema := &plugin.DBSchema{}
	for i := range 200 {
		schema.Tables = append(schema.Tables, plugin.Table{Schema: "public", Name: fmt.Sprintf("table_%03d", i), Kind: "table", Columns: []plugin.TableColumn{
			{Name: "id", DBType: "bigint"}, {Name: "note", DBType: "text", Nullable: true, Comment: "free */ text\nwith lines"},
		}})
	}
	schema.Tables = append(schema.Tables, plugin.Table{Schema: "hr", Name: "salaries", Columns: []plugin.TableColumn{{Name: "amount", DBType: "numeric"}}})

	text, truncated, omitted := renderSchema(schema, []string{"hr.salaries"}, "count rows of TABLE_199 and table_150", 2000)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if !truncated || omitted == 0 || len(text) > 2000 {
		t.Fatalf("truncated %v omitted %d len %d", truncated, omitted, len(text))
	}
	if !strings.HasPrefix(lines[0], "public.table_150:") || !strings.HasPrefix(lines[1], "public.table_199:") {
		t.Errorf("mentioned tables do not come first: %q", lines[:2])
	}
	if strings.Contains(text, "salaries") || strings.Contains(text, "*/ text") || strings.Contains(lines[0], "\nwith") {
		t.Errorf("schema text %q", lines[0])
	}
	if full, truncated, _ := renderSchema(schema, nil, "", SchemaBudget); truncated || !strings.Contains(full, "hr.salaries: amount numeric not null") {
		t.Errorf("full schema truncated=%v", truncated)
	}
}

func TestReadOnly(t *testing.T) {
	cases := map[string]bool{
		"select * from orders":                                  true,
		"WITH t AS (SELECT 1) SELECT * FROM t;":                 true,
		"select 'delete from x' as s -- update later":           true,
		`select "update" from t`:                                true,
		"select created_at, updated_by from orders":             true,
		"delete from orders":                                    false,
		"with d as (delete from t returning *) select * from d": false,
		"select * into backup from orders":                      false,
		"select 1; drop table orders":                           false,
		"  \n update orders set total = 0":                      false,
	}
	for sql, want := range cases {
		if got := readOnly(sqlscan.Postgres, sql); got != want {
			t.Errorf("%q: %v, want %v", sql, got, want)
		}
	}
}
