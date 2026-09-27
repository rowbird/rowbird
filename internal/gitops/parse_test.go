package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `apiVersion: rowbird.dev/v1
kind: Connection
metadata:
  name: production-db
spec:
  driver: postgres
  config: { host: db.internal, port: 5432, database: shop }
  secrets:
    password: ${env:DB_PASSWORD}
  options: { queryTimeoutSeconds: 60, maxRows: 100000, aiExcludedTables: [salaries] }
---
# an empty document is skipped
---
apiVersion: rowbird.dev/v1
kind: Channel
metadata: { name: sales-slack }
spec:
  type: slack
  secrets: { bot_token: "${env:SLACK_BOT_TOKEN}" }
---
apiVersion: rowbird.dev/v1
kind: Report
metadata:
  name: daily-sales
  title: Daily sales
  description: Sales by region, yesterday
spec:
  query:
    connection: production-db
    sql: |
      SELECT region, SUM(total) AS total FROM orders
      WHERE created_at >= {{yesterday}} AND created_at < {{today}}
      GROUP BY region
    params:
      - { name: region, type: text, default: south }
  schedule: { cron: "0 7 * * 1-5", timezone: America/Sao_Paulo }
  condition: { match: all, rules: [ { type: row_count, op: gt, value: 0 } ] }
  params: { region: north }
  run: { retryMax: 3, misfire: skip }
  deliveries:
    - channel: sales-slack
      mode: link
      formats: [xlsx]
      link: { expiresIn: 30d, requireLogin: true }
      options: { channel: "#sales" }
`

func TestParse(t *testing.T) {
	docs, err := ParseSet("sample.yaml", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 || docs[0].Connection == nil || docs[1].Channel == nil || docs[2].Report == nil {
		t.Fatalf("docs %+v", docs)
	}
	c := docs[0].Connection
	if c.Config["port"] != 5432 || *c.Options.MaxRows != 100000 || c.Options.AIExcludedTables[0] != "salaries" {
		t.Errorf("connection %+v", c)
	}
	if name, ok := EnvName(c.Secrets["password"]); !ok || name != "DB_PASSWORD" {
		t.Errorf("secret %v", c.Secrets["password"])
	}
	r := docs[2].Report
	if !r.Query.Inline() || !strings.Contains(r.Query.SQL, "{{yesterday}}") || *r.Query.Params[0].Default != "south" || r.Params["region"] != "north" {
		t.Errorf("query %+v", r.Query)
	}
	if r.Condition.Rules[0]["op"] != "gt" || *r.Run.RetryMax != 3 || *r.Run.Misfire != "skip" {
		t.Errorf("report %+v", r)
	}
	d := r.Deliveries[0]
	if d.Link.ExpiresIn != "30d" || !*d.Link.RequireLogin || d.Options["channel"] != "#sales" || docs[2].Metadata.Title != "Daily sales" {
		t.Errorf("delivery %+v", d)
	}
	if docs[2].Pos.Line != 21 || docs[2].Pos.File != "sample.yaml" {
		t.Errorf("position %v", docs[2].Pos)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name, yaml, want string
	}{
		{"version", "apiVersion: rowbird.dev/v0\nkind: Query\nmetadata: {name: q}\nspec: {}\n", "f.yaml:1:1: apiVersion must be rowbird.dev/v1"},
		{"kind", "apiVersion: rowbird.dev/v1\nkind: Dashboard\nmetadata: {name: q}\nspec: {}\n", "kind must be one of"},
		{"unknown field", "apiVersion: rowbird.dev/v1\nkind: Query\nmetadata: {name: q}\nspec:\n  connection: db\n  sql: select 1\n  sqll: x\n", `f.yaml:7:3: unknown field "sqll"`},
		{"nested unknown", "apiVersion: rowbird.dev/v1\nkind: Connection\nmetadata: {name: c}\nspec:\n  driver: sqlite\n  options: {maxrows: 5}\n", `f.yaml:6:13: unknown field "maxrows"`},
		{"type", "apiVersion: rowbird.dev/v1\nkind: Connection\nmetadata: {name: c}\nspec:\n  driver: sqlite\n  options: {maxRows: many}\n", "f.yaml:6: cannot unmarshal"},
		{"name", "apiVersion: rowbird.dev/v1\nkind: Query\nmetadata: {name: Bad Name}\nspec: {connection: db, sql: select 1}\n", "metadata.name must be"},
		{"required", "apiVersion: rowbird.dev/v1\nkind: Query\nmetadata: {name: q}\nspec: {connection: db}\n", "spec.sql is required"},
		{"ref and inline", "apiVersion: rowbird.dev/v1\nkind: Report\nmetadata: {name: r}\nspec:\n  query: {ref: q, sql: select 1}\n  schedule: {cron: '@daily'}\n", "either ref or an inline query"},
		{"duration", "apiVersion: rowbird.dev/v1\nkind: Report\nmetadata: {name: r}\nspec:\n  query: {ref: q}\n  schedule: {cron: '@daily'}\n  deliveries: [{channel: c, mode: link, link: {expiresIn: 2w}}]\n", "is not a duration"},
		{"syntax", "apiVersion: rowbird.dev/v1\nkind: [\n", "f.yaml:2: did not find expected node content"},
	}
	for _, c := range cases {
		_, err := ParseSet("f.yaml", []byte(c.yaml))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

func TestParseFilesReportsDuplicatesAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	q := "apiVersion: rowbird.dev/v1\nkind: Query\nmetadata: {name: q}\nspec: {connection: db, sql: select 1}\n"
	_ = os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(q), 0o600)
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "sub", "b.yml"), []byte(q), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o600)
	_, err := ParseFiles(dir)
	es, ok := AsErrors(err)
	if !ok || len(es) != 1 || !strings.HasSuffix(es[0].Pos.File, "b.yml") || !strings.Contains(es[0].Message, "a.yaml:1:1") {
		t.Fatalf("errors %v", err)
	}
}

func TestDurations(t *testing.T) {
	for in, want := range map[string]int{"30d": 2592000, "12h": 43200, "90m": 5400, "45s": 45} {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("%s: %d %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "0d", "-1h", "2w", "d"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	for secs, want := range map[int]string{604800: "7d", 7200: "2h", 5400: "90m", 61: "61s"} {
		if got := FormatDuration(secs); got != want {
			t.Errorf("%d: %s, want %s", secs, got, want)
		}
	}
}
