package ai

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rowbird/rowbird/internal/params"
	"github.com/rowbird/rowbird/internal/schedule"
	"github.com/rowbird/rowbird/internal/sqlscan"
)

// Warnings a proposal can carry. They inform; the user decides.
const (
	WarnNotReadOnly     = "ai.warning.not_read_only"
	WarnMultiple        = "ai.warning.multiple_statements"
	WarnScheduleInvalid = "ai.warning.schedule_invalid"
	WarnSchemaTruncated = "ai.warning.schema_truncated"
	WarnNoSchedule      = "ai.warning.no_schedule"
)

// MaxNameChars bounds the suggested name.
const MaxNameChars = 200

// Proposal is what the assistant suggests. It is never executed or saved by the server.
type Proposal struct {
	Task          string
	SQL           string
	Explanation   string
	SuggestedName string
	// Params are the named parameters the SQL uses that are not built-in, suggested as text.
	Params   []params.Definition
	Schedule *ScheduleProposal
	Warnings []string
	Model    string
}

// ScheduleProposal is a validated schedule with its description and next runs.
type ScheduleProposal struct {
	Cron        string
	Timezone    string
	Description string
	Next        []time.Time
}

type answer struct {
	SQL           string `json:"sql"`
	Explanation   string `json:"explanation"`
	SuggestedName string `json:"suggested_name"`
	Cron          string `json:"cron"`
	Timezone      string `json:"timezone"`
	Schedule      *struct {
		Cron     string `json:"cron"`
		Timezone string `json:"timezone"`
	} `json:"schedule"`
}

// validate turns the model's JSON into a proposal. It fails only when the answer is unusable (not
// the expected object, or a query task without SQL); doubtful content becomes warnings.
func validate(in Input, pr prompt, raw []byte, now time.Time) (*Proposal, error) {
	var a answer
	if err := decode(raw, &a); err != nil {
		return nil, fmt.Errorf("decode answer: %w", err)
	}
	p := &Proposal{Task: in.Task, Explanation: strings.TrimSpace(a.Explanation), Warnings: []string{}, Params: []params.Definition{}}
	if in.Task == TaskSchedule {
		if strings.TrimSpace(a.Cron) == "" {
			p.Warnings = append(p.Warnings, WarnNoSchedule)
			return p, nil
		}
		p.Schedule = scheduleProposal(a.Cron, a.Timezone, in, now)
		if p.Schedule == nil {
			p.Warnings = append(p.Warnings, WarnScheduleInvalid)
		}
		return p, nil
	}
	p.SQL = strings.TrimSpace(a.SQL)
	if p.SQL == "" {
		return nil, errors.New("the answer has no SQL")
	}
	p.SuggestedName = strings.TrimSpace(a.SuggestedName)
	if r := []rune(p.SuggestedName); len(r) > MaxNameChars {
		p.SuggestedName = string(r[:MaxNameChars])
	}
	stmts := sqlscan.Statements(pr.dialect, p.SQL)
	if len(stmts) > 1 {
		p.Warnings = append(p.Warnings, WarnMultiple)
	}
	if !readOnly(pr.dialect, p.SQL) {
		p.Warnings = append(p.Warnings, WarnNotReadOnly)
	}
	for _, name := range params.Names(pr.dialect, p.SQL) {
		if !params.IsBuiltin(name) {
			p.Params = append(p.Params, params.Definition{Name: name, Type: params.Text})
		}
	}
	if pr.truncated {
		p.Warnings = append(p.Warnings, WarnSchemaTruncated)
	}
	if a.Schedule != nil && strings.TrimSpace(a.Schedule.Cron) != "" {
		p.Schedule = scheduleProposal(a.Schedule.Cron, a.Schedule.Timezone, in, now)
		if p.Schedule == nil {
			p.Warnings = append(p.Warnings, WarnScheduleInvalid)
		}
	}
	return p, nil
}

func scheduleProposal(cron, tz string, in Input, now time.Time) *ScheduleProposal {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		tz = in.Timezone
	}
	if tz == "" {
		tz = "UTC"
	}
	s, err := schedule.Parse(cron, tz)
	if err != nil {
		return nil
	}
	return &ScheduleProposal{Cron: s.Expression(), Timezone: tz, Description: schedule.Describe(s.Expression(), in.Locale), Next: s.NextN(now, 3)}
}

var (
	firstWord = regexp.MustCompile(`^\s*([A-Za-z]+)`)
	writes    = regexp.MustCompile(`(?i)\b(insert|update|delete|merge|upsert|replace|drop|alter|create|truncate|grant|revoke|exec|execute|call|copy|into|lock|vacuum|attach|pragma)\b`)
	reads     = []string{"select", "with", "values", "table", "show", "explain", "describe"}
)

// readOnly guesses whether sql only reads: every statement starts like a read and no code outside
// strings, quoted names and comments has a writing keyword. It is a warning, not a guarantee:
// queries still run in read-only transactions.
func readOnly(d sqlscan.Dialect, sql string) bool {
	var code strings.Builder
	for _, t := range sqlscan.Tokenize(d, sql) {
		switch t.Kind {
		case sqlscan.Code:
			code.WriteString(t.Text)
		case sqlscan.Semicolon, sqlscan.BatchSeparator:
			code.WriteString(";")
		default:
			code.WriteString(" ")
		}
	}
	text := code.String()
	for _, stmt := range strings.Split(text, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		m := firstWord.FindStringSubmatch(stmt)
		if m == nil || !slices.Contains(reads, strings.ToLower(m[1])) {
			return false
		}
	}
	return !writes.MatchString(text)
}
