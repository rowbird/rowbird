package msgtemplate

import (
	"os"
	"path/filepath"
	"testing"
)

var vars = Vars{
	ReportTitle: `Sales <Q3> & "more"`, ReportSlug: "sales", ReportURL: "https://rb.example.com/reports/1",
	RunID: "r1", RunURL: "https://rb.example.com/runs/r1", RunStatus: "succeeded", RunRows: "1,234",
	RunDuration: "1.2 s", RunStarted: "2026-09-25 10:30", RunDate: "2026-09-25", RunTruncated: true,
	LinkURL: "https://rb.example.com/r/rbl_x", LinkExpiresAt: "2026-10-02", Format: "xlsx",
}

func TestRender(t *testing.T) {
	cases := []struct {
		name, src string
		esc       Escape
		want      string
	}{
		{"html escapes", "{{report.name}} ({{run.rows}} rows)", HTML, "Sales &lt;Q3&gt; &amp; &#34;more&#34; (1,234 rows)"},
		{"triple mustache still escapes", "{{{report.name}}}|{{&report.name}}", HTML, "Sales &lt;Q3&gt; &amp; &#34;more&#34;|Sales &lt;Q3&gt; &amp; &#34;more&#34;"},
		{"slack", "{{report.name}}", Slack, `Sales &lt;Q3&gt; &amp; "more"`},
		{"none", "{{report.name}}", None, `Sales <Q3> & "more"`},
		{"sections", "{{#run.truncated}}cut{{/run.truncated}}{{^link.url}}no link{{/link.url}}", None, "cut"},
		{"empty optional", "[{{condition.summary}}]{{^condition.summary}}none{{/condition.summary}}", None, "[]none"},
		{"path", "reports/{{report.slug}}/{{run.date}}/{{report.slug}}.{{format}}", None, "reports/sales/2026-09-25/sales.xlsx"},
	}
	for _, tc := range cases {
		got, err := Render(tc.src, vars, tc.esc)
		if err != nil || got != tc.want {
			t.Errorf("%s: %q %v, want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	// A partial must never read a file, even one next to the process.
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "secret.mustache"), []byte("leaked"), 0o600)
	t.Setenv("CWD", dir)
	cases := map[string]string{
		"{{report.name}}":          "",
		"{{#run}}{{rows}}{{/run}}": "validation.template_variable",
		"{{password}}":             CodeVariable,
		"{{> secret}}":             CodeSyntax,
		"{{#report.name}}{{nope}}{{/report.name}}": CodeVariable,
		"{{report.name":                   CodeSyntax,
		string(make([]byte, MaxLength+1)): CodeTooLong,
	}
	for src, want := range cases {
		err := Validate(src)
		te, ok := IsTemplateError(err)
		switch {
		case want == "" && err != nil:
			t.Errorf("%.30q: %v", src, err)
		case want != "" && (!ok || te.Code != want):
			t.Errorf("%.30q: %v, want %s", src, err, want)
		}
	}
	if out, err := Render("x{{> secret}}", vars, None); err == nil || out != "" {
		t.Errorf("partial rendered %q", out)
	}
}
