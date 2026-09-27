package plugin

import (
	"context"
	"time"
)

// Condition decides whether a run goes on to delivery (docs/spec/04-plugins.md). A report stores a
// list of rules; each rule names a condition and carries that condition's parameters, validated by
// its ConfigSchema.
type Condition interface {
	Plugin
	// Evaluate checks one rule. params were validated by ConfigSchema. An error means the rule
	// cannot be evaluated (a missing column, a value of the wrong type) and fails the run; a rule
	// that simply does not hold returns Passed false.
	Evaluate(ctx context.Context, in ConditionInput, params map[string]any) (RuleOutcome, error)
}

// ConditionInput is what a condition sees of a run's result.
type ConditionInput struct {
	Columns []Column
	// FirstRow is nil when the result is empty.
	FirstRow []any
	RowCount int64
	// Truncated is true when the row limit cut the result, so RowCount is a lower bound.
	Truncated bool
	// ResultHash identifies the result; PreviousHash is the hash of the last delivered result, empty
	// when there is none.
	ResultHash   string
	PreviousHash string
	// Location is the report's time zone, used to read local date and time values.
	Location *time.Location
}

// RuleOutcome is the result of one rule. Detail holds what the UI shows next to it (the actual
// value, the count compared, ...), as JSON-friendly values.
type RuleOutcome struct {
	Passed bool           `json:"passed"`
	Detail map[string]any `json:"detail,omitempty"`
}

// ConditionCapabilities describe a condition to the UI.
type ConditionCapabilities struct {
	// NeedsColumn is true when the rule refers to a result column (the UI offers a column picker).
	NeedsColumn bool `json:"needs_column"`
}

// Condition error codes.
const (
	ErrCodeConditionColumn = "condition.column_not_found"
	ErrCodeConditionValue  = "condition.invalid_value"
)

// ConditionError is a rule that cannot be evaluated, with a stable code and safe detail.
type ConditionError struct {
	Code   string
	Detail map[string]any
}

func (e *ConditionError) Error() string { return e.Code }
