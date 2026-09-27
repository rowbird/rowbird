package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestEveryVariableIsDocumented keeps the configuration page of the docs site in step with Config:
// every field, as its ROWBIRD_* variable, must appear there.
func TestEveryVariableIsDocumented(t *testing.T) {
	page, err := os.ReadFile("../../docs/site/operations/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range envNames(reflect.TypeFor[Config](), "") {
		if !strings.Contains(string(page), "`"+name+"`") {
			t.Errorf("%s is not documented in docs/site/operations/configuration.md", name)
		}
	}
}

func envNames(t reflect.Type, prefix string) []string {
	var names []string
	for f := range t.Fields() {
		key := f.Tag.Get("koanf")
		if key == "" {
			continue
		}
		if f.Type.Kind() == reflect.Struct && f.Type.Name() == "StorageS3" {
			names = append(names, envNames(f.Type, prefix+key+"_")...)
			continue
		}
		names = append(names, EnvPrefix+strings.ToUpper(prefix+key))
	}
	return names
}
