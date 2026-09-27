package plugin

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/rowbird/rowbird/internal/apperr"
)

type fake struct{ id string }

func (f fake) Meta() Metadata        { return Metadata{ID: f.id, Name: "plugin." + f.id + ".name"} }
func (f fake) ConfigSchema() *Schema { return &Schema{} }
func (f fake) Capabilities() any     { return nil }
func (f fake) Messages() Messages    { return nil }

func TestRegistry(t *testing.T) {
	Register(KindFormatter, "zz-test-b", func() Plugin { return fake{"zz-test-b"} })
	Register(KindFormatter, "zz-test-a", func() Plugin { return fake{"zz-test-a"} })
	t.Cleanup(func() {
		unregister(KindFormatter, "zz-test-a")
		unregister(KindFormatter, "zz-test-b")
	})

	if p, ok := Get(KindFormatter, "zz-test-a"); !ok || p.Meta().ID != "zz-test-a" {
		t.Fatal("Get failed")
	}
	if _, ok := Get(KindConnector, "zz-test-a"); ok {
		t.Fatal("kinds are not separate")
	}
	list := List(KindFormatter)
	if len(list) < 2 || list[0].Meta().ID > list[1].Meta().ID {
		t.Fatal("List is not sorted")
	}

	mustPanic(t, "duplicate", func() { Register(KindFormatter, "zz-test-a", func() Plugin { return fake{"zz-test-a"} }) })
	mustPanic(t, "id mismatch", func() { Register(KindFormatter, "zz-test-c", func() Plugin { return fake{"other"} }) })
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected a panic", name)
		}
	}()
	fn()
}

var testSchema = &Schema{Fields: []Field{
	{Key: "host", Type: TypeString, Required: true, Label: "l.host", Format: "hostname", MaxLength: 10},
	{Key: "port", Type: TypeInteger, Default: 5432, Minimum: Float(1), Maximum: Float(65535), Label: "l.port"},
	{Key: "password", Type: TypeString, Secret: true, Label: "l.password"},
	{Key: "mode", Type: TypeString, Enum: []string{"disable", "verify-ca"}, Default: "disable", Label: "l.mode"},
	{Key: "ca", Type: TypeString, Multiline: true, ShowIf: &ShowIf{Field: "mode", In: []any{"verify-ca"}}, Required: true, Label: "l.ca"},
	{Key: "ratio", Type: TypeNumber, Label: "l.ratio"},
	{Key: "enabled", Type: TypeBoolean, Default: false, Label: "l.enabled"},
}}

func TestValidate(t *testing.T) {
	got, err := testSchema.Validate(map[string]any{"host": "  db  ", "password": " spaced ", "ratio": 1.5})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"host": "db", "port": int64(5432), "password": " spaced ", "mode": "disable", "ratio": 1.5, "enabled": false}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v, want %#v", k, got[k], v)
		}
	}

	// The hidden field is dropped even when sent.
	got, _ = testSchema.Validate(map[string]any{"host": "db", "ca": "x"})
	if _, ok := got["ca"]; ok {
		t.Error("hidden field kept")
	}

	cases := map[string]struct {
		values map[string]any
		field  string
		code   string
	}{
		"required":       {map[string]any{}, "host", CodeRequired},
		"empty string":   {map[string]any{"host": ""}, "host", CodeRequired},
		"too long":       {map[string]any{"host": "a-very-long-host"}, "host", CodeTooLong},
		"wrong type":     {map[string]any{"host": 12}, "host", CodeInvalidType},
		"fraction":       {map[string]any{"host": "db", "port": 1.5}, "port", CodeInvalidType},
		"range":          {map[string]any{"host": "db", "port": 70000.0}, "port", CodeOutOfRange},
		"enum":           {map[string]any{"host": "db", "mode": "yolo"}, "mode", CodeInvalidValue},
		"shown required": {map[string]any{"host": "db", "mode": "verify-ca"}, "ca", CodeRequired},
		"unknown field":  {map[string]any{"host": "db", "sneaky": 1}, "sneaky", CodeUnknownField},
		"bool type":      {map[string]any{"host": "db", "enabled": "yes"}, "enabled", CodeInvalidType},
		"json number":    {map[string]any{"host": "db", "port": json.Number("x")}, "port", CodeInvalidType},
	}
	for name, tc := range cases {
		_, err := testSchema.Validate(tc.values)
		e, ok := apperr.As(err)
		if !ok {
			t.Errorf("%s: no validation error", name)
			continue
		}
		found := false
		for _, f := range e.Fields {
			found = found || (f.Field == tc.field && f.Code == tc.code)
		}
		if !found {
			t.Errorf("%s: fields %+v lack %s=%s", name, e.Fields, tc.field, tc.code)
		}
	}
}

func TestSplitAndSecrets(t *testing.T) {
	cfg, sec := testSchema.Split(map[string]any{"host": "db", "password": "pw"})
	if cfg["host"] != "db" || sec["password"] != "pw" || cfg["password"] != nil || len(testSchema.SecretKeys()) != 1 {
		t.Fatalf("cfg %v sec %v", cfg, sec)
	}
}

func TestSchemaJSON(t *testing.T) {
	b, err := json.Marshal(testSchema)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Type       string                    `json:"type"`
		Order      []string                  `json:"x-order"`
		Required   []string                  `json:"required"`
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Type != "object" || doc.Order[0] != "host" || doc.Order[6] != "enabled" || doc.Required[0] != "host" {
		t.Fatalf("doc %+v", doc)
	}
	if doc.Properties["password"]["x-secret"] != true || doc.Properties["ca"]["x-show-if"] == nil || doc.Properties["port"]["default"] != 5432.0 {
		t.Fatalf("properties %+v", doc.Properties)
	}
}

func TestConnError(t *testing.T) {
	base := errors.New("pq: password authentication failed")
	err := error(NewConnError(ErrCodeAuthFailed, base))
	ce, ok := AsConnError(err)
	if !ok || ce.Code != ErrCodeAuthFailed || !errors.Is(err, base) {
		t.Fatal("ConnError wrapping")
	}
}

func TestMergeMessages(t *testing.T) {
	m := MergeMessages(Messages{"en": {"a": "1"}}, Messages{"en": {"b": "2"}, "pt-BR": {"a": "um"}})
	if m["en"]["a"] != "1" || m["en"]["b"] != "2" || m["pt-BR"]["a"] != "um" {
		t.Fatalf("%v", m)
	}
}
