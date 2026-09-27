// Package msgtemplate renders the Mustache templates of delivery messages: subjects, message texts
// and S3 paths (docs/spec/04-plugins.md, "Message templates").
//
// Templates are written by editors, so they are sandboxed: partials are disabled (the library
// would otherwise read them from disk), triple mustaches cannot switch escaping off, and only the
// documented variables exist.
package msgtemplate

import (
	"errors"
	"fmt"
	"html"
	"slices"
	"strings"

	"github.com/cbroglie/mustache"
)

// Escape says how values are escaped for a destination.
type Escape int

// Escapes.
const (
	// HTML escapes for email bodies and Telegram's HTML mode.
	HTML Escape = iota
	// Slack escapes &, < and >, as Slack's message format requires.
	Slack
	// None leaves values as they are (plain text, Discord, JSON built by the webhook itself, paths).
	None
)

// MaxLength bounds a template.
const MaxLength = 10_000

// Validation codes.
const (
	CodeSyntax   = "validation.template"
	CodeVariable = "validation.template_variable"
	CodeTooLong  = "validation.too_long"
)

// Variables lists every variable a template may use.
var Variables = []string{
	"report.name", "report.title", "report.slug", "report.url",
	"run.id", "run.url", "run.status", "run.rows", "run.truncated", "run.duration", "run.started_at", "run.date",
	"link.url", "link.expires_at", "condition.summary", "format",
}

// sections that may open a block ({{#run.truncated}} ... {{/run.truncated}}).
var sections = append([]string{"report", "run", "link", "condition"}, Variables...)

var empty = &mustache.StaticProvider{Partials: map[string]string{}}

// ErrTemplate is a template that does not parse or uses unknown variables. Code is a validation
// code; Detail names the offending variable.
type ErrTemplate struct {
	Code   string
	Detail string
}

func (e *ErrTemplate) Error() string { return e.Code + ": " + e.Detail }

func parse(src string) (*mustache.Template, error) {
	if len(src) > MaxLength {
		return nil, &ErrTemplate{Code: CodeTooLong}
	}
	// {{{x}}} and {{&x}} would print a value without escaping; they are read as {{x}}.
	src = strings.NewReplacer("{{{", "{{", "}}}", "}}", "{{&", "{{").Replace(src)
	t, err := mustache.ParseStringPartials(src, empty)
	if err != nil {
		return nil, &ErrTemplate{Code: CodeSyntax, Detail: err.Error()}
	}
	if err := checkTags(t.Tags()); err != nil {
		return nil, err
	}
	return t, nil
}

func checkTags(tags []mustache.Tag) error {
	for _, tag := range tags {
		switch tag.Type() {
		case mustache.Partial:
			return &ErrTemplate{Code: CodeSyntax, Detail: "partials are not supported"}
		case mustache.Variable:
			if !slices.Contains(Variables, tag.Name()) {
				return &ErrTemplate{Code: CodeVariable, Detail: tag.Name()}
			}
		case mustache.Section, mustache.InvertedSection:
			if !slices.Contains(sections, tag.Name()) {
				return &ErrTemplate{Code: CodeVariable, Detail: tag.Name()}
			}
			if err := checkTags(tag.Tags()); err != nil {
				return err
			}
		}
	}
	return nil
}

// Validate checks a template without rendering it.
func Validate(src string) error {
	_, err := parse(src)
	return err
}

// Render renders src with vars, escaping values for the destination.
func Render(src string, vars Vars, esc Escape) (string, error) {
	t, err := parse(src)
	if err != nil {
		return "", err
	}
	switch esc {
	case HTML:
		t.Escape(html.EscapeString)
	case Slack:
		t.Escape(strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace)
	default:
		t.Escape(func(s string) string { return s })
	}
	out, err := t.Render(vars.context())
	if err != nil {
		return "", fmt.Errorf("msgtemplate: render: %w", err)
	}
	return out, nil
}

// Vars are the values templates see, already formatted for the reader's language.
type Vars struct {
	ReportTitle, ReportSlug, ReportURL                string
	RunID, RunURL, RunStatus, RunDuration, RunStarted string
	RunDate                                           string
	RunRows                                           string
	RunTruncated                                      bool
	LinkURL, LinkExpiresAt, ConditionSummary, Format  string
}

func (v Vars) context() map[string]any {
	return map[string]any{
		"report": map[string]any{"name": v.ReportTitle, "title": v.ReportTitle, "slug": v.ReportSlug, "url": v.ReportURL},
		"run": map[string]any{
			"id": v.RunID, "url": v.RunURL, "status": v.RunStatus, "rows": v.RunRows, "truncated": v.RunTruncated,
			"duration": v.RunDuration, "started_at": v.RunStarted, "date": v.RunDate,
		},
		"link":      map[string]any{"url": v.LinkURL, "expires_at": v.LinkExpiresAt},
		"condition": map[string]any{"summary": v.ConditionSummary},
		"format":    v.Format,
	}
}

// IsTemplateError reports whether err is a template problem, returning it.
func IsTemplateError(err error) (*ErrTemplate, bool) {
	var te *ErrTemplate
	ok := errors.As(err, &te)
	return te, ok
}
