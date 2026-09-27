package security

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestSecretNeverPrints(t *testing.T) {
	const value = "hunter2-super-secret"
	s := Secret(value)

	var logBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&logBuf, nil)).Info("msg", "s", s)
	jsonBytes, err := json.Marshal(struct{ S Secret }{s})
	if err != nil {
		t.Fatal(err)
	}

	outputs := map[string]string{
		"%s":   fmt.Sprintf("%s", s), //nolint:staticcheck // the verb itself is under test
		"%v":   fmt.Sprintf("%v", s),
		"%+v":  fmt.Sprintf("%+v", struct{ S Secret }{s}),
		"%#v":  fmt.Sprintf("%#v", s),
		"%q":   fmt.Sprintf("%q", s),
		"json": string(jsonBytes),
		"slog": logBuf.String(),
	}
	for name, out := range outputs {
		if strings.Contains(out, value) {
			t.Errorf("%s leaked the secret: %s", name, out)
		}
		if !strings.Contains(out, Redacted) {
			t.Errorf("%s does not show the placeholder: %s", name, out)
		}
	}
	if s.Reveal() != value {
		t.Errorf("Reveal() = %q", s.Reveal())
	}
	if !s.IsSet() || Secret("").IsSet() {
		t.Error("IsSet is wrong")
	}
}
