package json

import (
	stdjson "encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin/formattest"
)

func TestConformance(t *testing.T) {
	formattest.Run(t, formattest.Harness{
		Formatter: formatter{}, Dir: "testdata/golden",
		Options: map[string]map[string]any{"ndjson": {"layout": "ndjson", "decimals": "number"}},
	})
}

// TestGoldenFilesAreValid parses what the golden files hold.
func TestGoldenFilesAreValid(t *testing.T) {
	for _, name := range []string{"sample.en.json", "empty.en.json"} {
		b, err := os.ReadFile("testdata/golden/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var v []map[string]any
		if err := stdjson.Unmarshal(b, &v); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	b, _ := os.ReadFile("testdata/golden/sample.ndjson.en.json")
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if !stdjson.Valid([]byte(line)) {
			t.Errorf("invalid line %q", line)
		}
	}
}
