package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLocales(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCheckDir(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"complete", map[string]string{"en.json": `{"a": {"b": "x"}}`, "pt-BR.json": `{"a": {"b": "y"}}`}, nil},
		{"missing key", map[string]string{"en.json": `{"a": "x", "b": "y"}`, "pt-BR.json": `{"a": "x"}`}, []string{"pt-BR.json: missing b"}},
		{"extra key", map[string]string{"en.json": `{"a": "x"}`, "pt-BR.json": `{"a": "x", "z": "y"}`}, []string{"pt-BR.json: unknown key z"}},
		{"empty message", map[string]string{"en.json": `{"a": "x"}`, "pt-BR.json": `{"a": " "}`}, []string{"pt-BR.json: empty message for a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems, err := checkDir(writeLocales(t, tc.files))
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != len(tc.want) {
				t.Fatalf("problems %v, want %v", problems, tc.want)
			}
			for i, want := range tc.want {
				if !strings.Contains(problems[i], want) {
					t.Errorf("problem %q does not contain %q", problems[i], want)
				}
			}
		})
	}
	if _, err := checkDir(writeLocales(t, map[string]string{"pt-BR.json": `{}`})); err == nil {
		t.Error("a directory without en.json must be an error")
	}
}
