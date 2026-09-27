// Package conditiontest is the conformance suite every condition plugin must pass: metadata and
// translations, a parameter schema that validates its own defaults, JSON-friendly outcomes and the
// plugin's own evaluation cases.
package conditiontest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin"
)

// Case is one evaluation. Params are validated by the plugin's schema first, as a report would.
type Case struct {
	Name   string
	Params map[string]any
	Input  plugin.ConditionInput
	Want   bool
	// WantErr is the ConditionError code expected instead of an outcome.
	WantErr string
}

// Run executes the suite for c.
func Run(t *testing.T, c plugin.Condition, cases []Case) {
	t.Run("metadata", func(t *testing.T) {
		m := c.Meta()
		if m.ID == "" || m.Version == "" || c.ConfigSchema() == nil {
			t.Fatal("metadata or schema missing")
		}
		if _, ok := c.Capabilities().(plugin.ConditionCapabilities); !ok {
			t.Errorf("capabilities are %T, want plugin.ConditionCapabilities", c.Capabilities())
		}
		msgs := c.Messages()
		keys := []string{m.Name, m.Description}
		for _, f := range c.ConfigSchema().Fields {
			if f.Label == "" {
				t.Errorf("field %s has no label", f.Key)
			}
			keys = append(keys, f.Label)
			if f.Help != "" {
				keys = append(keys, f.Help)
			}
			for _, v := range f.Enum {
				keys = append(keys, strings.TrimSuffix(f.Label, ".label")+"."+v)
			}
		}
		for _, locale := range []string{"en", "pt-BR"} {
			for _, k := range keys {
				if msgs[locale][k] == "" {
					t.Errorf("%s has no %s translation for %s", m.ID, locale, k)
				}
			}
		}
	})

	t.Run("defaults validate", func(t *testing.T) {
		values := map[string]any{}
		required := false
		for _, f := range c.ConfigSchema().Fields {
			if f.Required && f.Default == nil {
				required = true
			}
		}
		if _, err := c.ConfigSchema().Validate(values); err != nil && !required {
			t.Errorf("empty parameters with defaults do not validate: %v", err)
		}
	})

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			params, err := c.ConfigSchema().Validate(tc.Params)
			if err != nil {
				t.Fatalf("parameters do not validate: %v", err)
			}
			got, err := c.Evaluate(context.Background(), tc.Input, params)
			if tc.WantErr != "" {
				var ce *plugin.ConditionError
				if !errors.As(err, &ce) || ce.Code != tc.WantErr {
					t.Fatalf("error = %v, want code %s", err, tc.WantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if got.Passed != tc.Want {
				t.Errorf("passed = %v, want %v (detail %v)", got.Passed, tc.Want, got.Detail)
			}
			if _, err := json.Marshal(got); err != nil {
				t.Errorf("outcome is not JSON: %v", err)
			}
			again, _ := c.Evaluate(context.Background(), tc.Input, params)
			if !reflect.DeepEqual(got, again) {
				t.Errorf("evaluation is not deterministic: %v then %v", got, again)
			}
		})
	}
}
