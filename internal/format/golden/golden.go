// Package golden compares test output with files under testdata, and rewrites them when tests run
// with -update (docs/spec/10-quality-and-community.md: formatters are tested with golden files).
package golden

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files with the current output")

// Check fails t when got differs from the file at path (relative to the test's package).
func Check(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("read golden file (run the test with -update to create it): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output differs from %s (run with -update after checking the change)\n--- got ---\n%s\n--- want ---\n%s", path, clip(got), clip(want))
	}
}

func clip(b []byte) []byte {
	if len(b) > 4000 {
		return append(b[:4000:4000], "..."...)
	}
	return b
}
