package secretconfig

import (
	"crypto/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/plugin"
)

var schema = &plugin.Schema{Fields: []plugin.Field{
	{Key: "host", Type: plugin.TypeString, Required: true, Label: "l"},
	{Key: "port", Type: plugin.TypeInteger, Default: int64(25), Label: "l"},
	{Key: "password", Type: plugin.TypeString, Secret: true, Label: "l"},
	{Key: "token", Type: plugin.TypeString, Secret: true, Label: "l"},
}}

func codec(t *testing.T, kind string) *Codec {
	t.Helper()
	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	kr, err := crypto.NewKeyring(key)
	if err != nil {
		t.Fatal(err)
	}
	return New(kr, kind)
}

func TestSealAndOpen(t *testing.T) {
	c := codec(t, "channel")
	id := uuid.New()
	validated, err := schema.Validate(map[string]any{"host": "smtp", "password": "s3cret-value"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, enc, err := c.Seal(id, schema, validated)
	if err != nil || enc == nil || strings.Contains(*enc, "s3cret") || cfg["password"] != nil || cfg["host"] != "smtp" {
		t.Fatalf("seal %v %v %v", cfg, enc, err)
	}
	values, err := c.Values(id, schema, map[string]any{"host": "smtp", "port": float64(25)}, enc)
	if err != nil || values["password"] != "s3cret-value" || values["port"] != int64(25) {
		t.Fatalf("values %v %v", values, err)
	}
	// The ciphertext is bound to the id and to the kind of entity.
	if _, err := c.Secrets(uuid.New(), enc); err == nil {
		t.Error("secrets opened with another id")
	}
	other := &Codec{keyring: c.keyring, kind: "connection"}
	if _, err := other.Secrets(id, enc); err == nil {
		t.Error("secrets opened as another kind")
	}
	if cfg, enc, _ := c.Seal(id, schema, map[string]any{"host": "x"}); enc != nil || cfg["host"] != "x" {
		t.Error("no secrets still produced a ciphertext")
	}
}

func TestResolveAndConfigured(t *testing.T) {
	existing := map[string]any{"password": "old", "token": "tok"}
	in := map[string]any{"host": "h", "password": map[string]any{"configured": true}, "token": nil}
	got := Resolve(schema, in, existing)
	if !reflect.DeepEqual(got, map[string]any{"host": "h", "password": "old"}) {
		t.Errorf("resolve %v", got)
	}
	if got := Resolve(schema, map[string]any{"password": "new"}, existing); got["password"] != "new" || got["token"] != "tok" {
		t.Errorf("replace %v", got)
	}
	if got := Configured(schema, map[string]any{"password": "x", "token": ""}); !reflect.DeepEqual(got, map[string]bool{"password": true}) {
		t.Errorf("configured %v", got)
	}
}

func TestScrub(t *testing.T) {
	got := Scrub("auth failed for s3cret-value at host", schema, map[string]any{"password": "s3cret-value", "token": "ab"})
	if strings.Contains(got, "s3cret") || !strings.Contains(got, "at host") {
		t.Errorf("scrub %q", got)
	}
}
