// Package condition holds the built-in condition plugins and the evaluation of a report's
// condition: a list of rules combined with "all" or "any" (docs/spec/04-plugins.md).
//
// A condition is stored on the report as JSON:
//
//	{ "match": "all", "rules": [ { "type": "row_count", "op": "gt", "value": 0 } ] }
//
// Each rule names a condition plugin; its other keys are that plugin's parameters. No rules means
// the condition always holds.
package condition

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/plugin"
)

// Match modes.
const (
	MatchAll = "all"
	MatchAny = "any"
)

// MaxRules bounds the rules of one condition.
const MaxRules = 20

// Rule is one rule: a condition plugin id and its parameters.
type Rule struct {
	Type   string
	Params map[string]any
}

// MarshalJSON writes the rule flat: {"type": ..., <params>}.
func (r Rule) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(r.Params)+1)
	for k, v := range r.Params {
		out[k] = v
	}
	out["type"] = r.Type
	return json.Marshal(out)
}

// UnmarshalJSON reads a flat rule. Numbers stay json.Number so that schema validation can tell
// integers apart.
func (r *Rule) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return err
	}
	r.Type, _ = m["type"].(string)
	delete(m, "type")
	r.Params = m
	return nil
}

// Spec is a report's condition.
type Spec struct {
	Match string `json:"match"`
	Rules []Rule `json:"rules"`
}

// Always is the condition of a report without rules.
func Always() Spec { return Spec{Match: MatchAll, Rules: []Rule{}} }

// Validate checks spec against the registered conditions and returns it normalized (parameters
// with defaults applied and numbers converted). Field errors are reported under prefix, for
// example "condition.rules[0].value".
func Validate(spec Spec, prefix string) (Spec, error) {
	var errs []apperr.FieldError
	out := Spec{Match: spec.Match, Rules: make([]Rule, 0, len(spec.Rules))}
	if out.Match == "" {
		out.Match = MatchAll
	}
	if out.Match != MatchAll && out.Match != MatchAny {
		errs = append(errs, apperr.Field(prefix+".match", plugin.CodeInvalidValue))
	}
	if len(spec.Rules) > MaxRules {
		errs = append(errs, apperr.Field(prefix+".rules", "validation.too_many"))
	}
	for i, r := range spec.Rules {
		at := fmt.Sprintf("%s.rules[%d]", prefix, i)
		c, ok := get(r.Type)
		if !ok {
			errs = append(errs, apperr.Field(at+".type", plugin.CodeInvalidValue))
			continue
		}
		params, err := c.ConfigSchema().Validate(numbers(r.Params))
		if err != nil {
			if ae, ok := apperr.As(err); ok {
				for _, fe := range ae.Fields {
					errs = append(errs, apperr.Field(at+"."+fe.Field, fe.Code))
				}
				continue
			}
			return Spec{}, err
		}
		out.Rules = append(out.Rules, Rule{Type: r.Type, Params: params})
	}
	if len(errs) > 0 {
		return Spec{}, apperr.Invalid(errs...)
	}
	return out, nil
}

// numbers converts json.Number parameters for schema validation.
func numbers(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if n, ok := v.(json.Number); ok {
			if i, err := n.Int64(); err == nil {
				out[k] = i
			} else if f, err := n.Float64(); err == nil {
				out[k] = f
			} else {
				out[k] = v
			}
			continue
		}
		out[k] = v
	}
	return out
}

func get(id string) (plugin.Condition, bool) {
	p, ok := plugin.Get(plugin.KindCondition, id)
	if !ok {
		return nil, false
	}
	c, ok := p.(plugin.Condition)
	return c, ok
}

// RuleResult is the outcome of one rule.
type RuleResult struct {
	Type string `json:"type"`
	plugin.RuleOutcome
}

// Result is the outcome of a condition, stored on the run.
type Result struct {
	Passed bool         `json:"passed"`
	Match  string       `json:"match"`
	Rules  []RuleResult `json:"rules"`
}

// Evaluate runs every rule (so the run detail shows all of them) and combines the outcomes. spec
// must have been validated. The first rule that cannot be evaluated returns its error.
func Evaluate(ctx context.Context, spec Spec, in plugin.ConditionInput) (*Result, error) {
	res := &Result{Match: spec.Match, Rules: make([]RuleResult, 0, len(spec.Rules))}
	if res.Match == "" {
		res.Match = MatchAll
	}
	if len(spec.Rules) == 0 {
		res.Passed = true
		return res, nil
	}
	for _, r := range spec.Rules {
		c, ok := get(r.Type)
		if !ok {
			return nil, &plugin.ConditionError{Code: plugin.ErrCodeConditionValue, Detail: map[string]any{"type": r.Type}}
		}
		out, err := c.Evaluate(ctx, in, numbers(r.Params))
		if err != nil {
			return nil, err
		}
		res.Rules = append(res.Rules, RuleResult{Type: r.Type, RuleOutcome: out})
	}
	passed := func(r RuleResult) bool { return r.Passed }
	if res.Match == MatchAny {
		res.Passed = slices.ContainsFunc(res.Rules, passed)
	} else {
		res.Passed = !slices.ContainsFunc(res.Rules, func(r RuleResult) bool { return !r.Passed })
	}
	return res, nil
}
