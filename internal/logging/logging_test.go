package logging

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/security"
)

const secret = "s3cr3t-value-42"

func TestRedactionNeverLeaksSecrets(t *testing.T) {
	cases := []struct {
		name string
		log  func(l *slog.Logger)
	}{
		{"sensitive key", func(l *slog.Logger) { l.Info("m", "password", secret) }},
		{"sensitive key mixed case", func(l *slog.Logger) { l.Info("m", "SMTP_Password", secret) }},
		{"api key", func(l *slog.Logger) { l.Info("m", "api_key", secret) }},
		{"authorization", func(l *slog.Logger) { l.Info("m", "Authorization", "Bearer "+secret) }},
		{"dsn key", func(l *slog.Logger) { l.Info("m", "dsn", "anything "+secret) }},
		{"secret type", func(l *slog.Logger) { l.Info("m", "value", security.Secret(secret)) }},
		{"url in attribute", func(l *slog.Logger) { l.Info("m", "url", "postgres://rowbird:"+secret+"@db:5432/x") }},
		{"url in message", func(l *slog.Logger) { l.Info("connecting to postgres://rowbird:" + secret + "@db/x") }},
		{"keyword dsn", func(l *slog.Logger) { l.Info("m", "conn", "host=db user=u password="+secret) }},
		{"quoted keyword dsn", func(l *slog.Logger) { l.Info("m", "conn", "host=db password='"+secret+"' user=u") }},
		{"error with url", func(l *slog.Logger) {
			l.Error("m", "error", fmt.Errorf("dial: %w", errors.New("postgres://u:"+secret+"@h/db refused")))
		}},
		{"group", func(l *slog.Logger) { l.Info("m", slog.Group("smtp", "host", "mail", "password", secret)) }},
		{"nested group", func(l *slog.Logger) {
			l.Info("m", slog.Group("a", slog.Group("b", "token", secret)))
		}},
		{"with attrs", func(l *slog.Logger) { l.With("secret", secret).Info("m") }},
		{"with group", func(l *slog.Logger) { l.WithGroup("g").Info("m", "password", secret) }},
		{"with secret type", func(l *slog.Logger) { l.With("k", security.Secret(secret)).Info("m") }},
	}
	for _, format := range []string{FormatText, FormatJSON} {
		for _, tc := range cases {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				var buf bytes.Buffer
				l, err := New(&buf, "debug", format)
				if err != nil {
					t.Fatal(err)
				}
				tc.log(l)
				if strings.Contains(buf.String(), secret) {
					t.Fatalf("secret leaked: %s", buf.String())
				}
				if buf.Len() == 0 {
					t.Fatal("nothing was logged")
				}
			})
		}
	}
}

func TestRedactionKeepsUsefulContext(t *testing.T) {
	var buf bytes.Buffer
	l, err := New(&buf, "info", FormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	l.Info("opened", "url", "postgres://rowbird:pw@db:5432/app", "host", "db", "count", 3)
	out := buf.String()
	for _, want := range []string{`postgres://rowbird:***@db:5432/app`, `"host":"db"`, `"count":3`} {
		if !strings.Contains(out, want) {
			t.Errorf("output %s missing %s", out, want)
		}
	}
}

func TestRedactString(t *testing.T) {
	cases := map[string]string{
		"no credentials here":                      "no credentials here",
		"user@example.com":                         "user@example.com",
		"https://host/path?x=1":                    "https://host/path?x=1",
		"smtp://user:pw@mail:25":                   "smtp://user:***@mail:25",
		"postgres://:pw@h/db":                      "postgres://:***@h/db",
		"host=db password=abc sslmode=disable":     "host=db password=*** sslmode=disable",
		"a postgres://u:p@h/1 and mysql://v:q@h/2": "a postgres://u:***@h/1 and mysql://v:***@h/2",
	}
	for in, want := range cases {
		if got := RedactString(in); got != want {
			t.Errorf("RedactString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l, err := New(&buf, "warn", FormatText)
	if err != nil {
		t.Fatal(err)
	}
	l.Info("hidden")
	l.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "shown") {
		t.Fatalf("unexpected output: %s", buf.String())
	}
}

func TestNewRejectsInvalidOptions(t *testing.T) {
	if _, err := New(&bytes.Buffer{}, "verbose", FormatText); err == nil {
		t.Error("expected error for invalid level")
	}
	if _, err := New(&bytes.Buffer{}, "info", "xml"); err == nil {
		t.Error("expected error for invalid format")
	}
}
