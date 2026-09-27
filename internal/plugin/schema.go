package plugin

import (
	"encoding/json"
	"math"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/rowbird/rowbird/internal/apperr"
)

// FieldType is the JSON type of a configuration field.
type FieldType string

// Field types supported by the schema subset.
const (
	TypeString  FieldType = "string"
	TypeInteger FieldType = "integer"
	TypeNumber  FieldType = "number"
	TypeBoolean FieldType = "boolean"
)

// ShowIf makes a field relevant only when another field has one of the given values. Hidden fields
// are neither validated nor stored.
type ShowIf struct {
	Field string `json:"field"`
	In    []any  `json:"in"`
}

// Field is one configuration field. Label and Help are i18n keys.
type Field struct {
	Key       string
	Type      FieldType
	Required  bool
	Default   any
	Enum      []string
	Minimum   *float64
	Maximum   *float64
	MinLength int
	MaxLength int
	// Format hints the UI and validation: "hostname", "path", "pem", "fingerprint", "url" (an
	// absolute http or https URL, validated).
	Format    string
	Secret    bool
	Group     string
	Multiline bool
	ShowIf    *ShowIf
	Label     string
	Help      string
}

// Schema is an ordered set of fields. It serializes to a JSON Schema subset with Rowbird
// extensions (x-order, x-secret, x-group, x-multiline, x-show-if, x-label, x-help), which the UI
// renders as a form (docs/spec/04-plugins.md).
type Schema struct {
	Fields []Field
}

// Field returns the field with key.
func (s *Schema) Field(key string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// SecretKeys lists the keys of secret fields.
func (s *Schema) SecretKeys() []string {
	var out []string
	for _, f := range s.Fields {
		if f.Secret {
			out = append(out, f.Key)
		}
	}
	return out
}

// Extend returns a schema with more fields appended (used to add the common TLS and SSH sections).
func (s *Schema) Extend(fields ...Field) *Schema {
	return &Schema{Fields: append(slices.Clone(s.Fields), fields...)}
}

// MarshalJSON renders the JSON Schema subset.
func (s *Schema) MarshalJSON() ([]byte, error) {
	props := map[string]any{}
	order := make([]string, 0, len(s.Fields))
	required := []string{}
	for _, f := range s.Fields {
		order = append(order, f.Key)
		if f.Required {
			required = append(required, f.Key)
		}
		p := map[string]any{"type": f.Type, "x-label": f.Label}
		if f.Help != "" {
			p["x-help"] = f.Help
		}
		if f.Default != nil {
			p["default"] = f.Default
		}
		if len(f.Enum) > 0 {
			p["enum"] = f.Enum
		}
		if f.Minimum != nil {
			p["minimum"] = *f.Minimum
		}
		if f.Maximum != nil {
			p["maximum"] = *f.Maximum
		}
		if f.MinLength > 0 {
			p["minLength"] = f.MinLength
		}
		if f.MaxLength > 0 {
			p["maxLength"] = f.MaxLength
		}
		if f.Format != "" {
			p["format"] = f.Format
		}
		if f.Secret {
			p["x-secret"] = true
			p["writeOnly"] = true
		}
		if f.Group != "" {
			p["x-group"] = f.Group
		}
		if f.Multiline {
			p["x-multiline"] = true
		}
		if f.ShowIf != nil {
			p["x-show-if"] = f.ShowIf
		}
		props[f.Key] = p
	}
	return json.Marshal(map[string]any{
		"type": "object", "properties": props, "required": required, "x-order": order,
		"additionalProperties": false,
	})
}

// Validation codes produced by Validate.
const (
	CodeRequired     = "validation.required"
	CodeInvalidType  = "validation.invalid_type"
	CodeInvalidValue = "validation.invalid_value"
	CodeTooShort     = "validation.too_short"
	CodeTooLong      = "validation.too_long"
	CodeOutOfRange   = "validation.range"
	CodeUnknownField = "validation.unknown_field"
	CodeInvalidURL   = "validation.url"
)

// Visible reports whether f applies given the other values.
func (s *Schema) Visible(f Field, values map[string]any) bool {
	if f.ShowIf == nil {
		return true
	}
	dep, _ := s.Field(f.ShowIf.Field)
	v, ok := values[f.ShowIf.Field]
	if !ok {
		v = dep.Default
	}
	if dep.ShowIf != nil && !s.Visible(dep, values) {
		return false
	}
	for _, want := range f.ShowIf.In {
		if v == want {
			return true
		}
	}
	return false
}

// Validate checks values against the schema and returns them normalized: defaults applied,
// numbers converted to int64 or float64, hidden fields dropped, strings trimmed (except secrets and
// multiline values). Field errors carry the field key and a validation code.
func (s *Schema) Validate(values map[string]any) (map[string]any, error) {
	var errs []apperr.FieldError
	out := map[string]any{}
	for k := range values {
		if _, ok := s.Field(k); !ok {
			errs = append(errs, apperr.Field(k, CodeUnknownField))
		}
	}
	// Defaults first, so ShowIf sees them.
	merged := map[string]any{}
	for _, f := range s.Fields {
		if v, ok := values[f.Key]; ok && v != nil {
			merged[f.Key] = v
		} else if f.Default != nil {
			merged[f.Key] = f.Default
		}
	}
	for _, f := range s.Fields {
		if !s.Visible(f, merged) {
			continue
		}
		v, present := merged[f.Key]
		if s, isStr := v.(string); present && isStr && s == "" {
			present = false
		}
		if !present {
			if f.Required {
				errs = append(errs, apperr.Field(f.Key, CodeRequired))
			}
			continue
		}
		nv, code := f.check(v)
		if code != "" {
			errs = append(errs, apperr.Field(f.Key, code))
			continue
		}
		out[f.Key] = nv
	}
	if len(errs) > 0 {
		slices.SortFunc(errs, func(a, b apperr.FieldError) int { return strings.Compare(a.Field, b.Field) })
		return nil, apperr.Invalid(errs...)
	}
	return out, nil
}

func (f Field) check(v any) (any, string) {
	switch f.Type {
	case TypeString:
		s, ok := v.(string)
		if !ok {
			return nil, CodeInvalidType
		}
		if !f.Secret && !f.Multiline {
			s = strings.TrimSpace(s)
		}
		n := utf8.RuneCountInString(s)
		switch {
		case f.MinLength > 0 && n < f.MinLength:
			return nil, CodeTooShort
		case f.MaxLength > 0 && n > f.MaxLength:
			return nil, CodeTooLong
		case len(f.Enum) > 0 && !slices.Contains(f.Enum, s):
			return nil, CodeInvalidValue
		case f.Format == "url" && !httpURL(s):
			return nil, CodeInvalidURL
		}
		return s, ""
	case TypeBoolean:
		b, ok := v.(bool)
		if !ok {
			return nil, CodeInvalidType
		}
		return b, ""
	case TypeInteger, TypeNumber:
		var n float64
		switch x := v.(type) {
		case float64:
			n = x
		case int:
			n = float64(x)
		case int64:
			n = float64(x)
		case json.Number:
			var err error
			if n, err = x.Float64(); err != nil {
				return nil, CodeInvalidType
			}
		default:
			return nil, CodeInvalidType
		}
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, CodeInvalidType
		}
		if f.Type == TypeInteger && n != math.Trunc(n) {
			return nil, CodeInvalidType
		}
		if (f.Minimum != nil && n < *f.Minimum) || (f.Maximum != nil && n > *f.Maximum) {
			return nil, CodeOutOfRange
		}
		if f.Type == TypeInteger {
			return int64(n), ""
		}
		return n, ""
	}
	return nil, CodeInvalidType
}

// Split separates validated values into plain configuration and secrets.
func (s *Schema) Split(values map[string]any) (config, secrets map[string]any) {
	config, secrets = map[string]any{}, map[string]any{}
	for k, v := range values {
		if f, ok := s.Field(k); ok && f.Secret {
			secrets[k] = v
		} else {
			config[k] = v
		}
	}
	return config, secrets
}

// Float returns a pointer to v, for Minimum and Maximum.
func Float(v float64) *float64 { return &v }

func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
