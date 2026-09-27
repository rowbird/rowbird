package i18n

import (
	"slices"
	"testing"
	"testing/fstest"
)

func TestEmbeddedCatalogsLoad(t *testing.T) {
	b := Default()
	if got := b.Locales(); !slices.Equal(got, []string{"en", "pt-BR"}) {
		t.Fatalf("locales %v", got)
	}
}

func TestCatalogsHaveTheSameKeys(t *testing.T) {
	b := Default()
	en := b.Keys(DefaultLocale)
	for _, loc := range b.Locales() {
		keys := b.Keys(loc)
		for k := range en {
			if _, ok := keys[k]; !ok {
				t.Errorf("%s is missing %s", loc, k)
			}
		}
		for k := range keys {
			if _, ok := en[k]; !ok {
				t.Errorf("%s has %s, which en does not", loc, k)
			}
		}
	}
}

func TestMatch(t *testing.T) {
	b := Default()
	cases := map[string]string{
		"":                        "en",
		"pt-BR,pt;q=0.9,en;q=0.8": "pt-BR",
		"pt":                      "pt-BR",
		"pt-PT":                   "pt-BR",
		"en-US":                   "en",
		"de-DE":                   "en",
		"fr;q=0.9, pt-BR;q=0.8":   "pt-BR",
		"garbage;;;":              "en",
	}
	for header, want := range cases {
		if got := b.Match(header); got != want {
			t.Errorf("Match(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestTranslateFallbacks(t *testing.T) {
	b, err := Load(fstest.MapFS{
		"locales/en.json":    {Data: []byte(`{"a": {"b": "en b", "c": "en c"}}`)},
		"locales/pt-BR.json": {Data: []byte(`{"a": {"b": "pt b"}}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := b.T("pt-BR", "a.b"); got != "pt b" {
		t.Errorf("got %q", got)
	}
	if got := b.T("pt-BR", "a.c"); got != "en c" {
		t.Errorf("missing key did not fall back to en: %q", got)
	}
	if got := b.T("pt-BR", "a.missing"); got != "a.missing" {
		t.Errorf("unknown key: %q", got)
	}
}

func TestLoadRejectsBadCatalogs(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"no default":   {"locales/pt-BR.json": {Data: []byte(`{}`)}},
		"invalid json": {"locales/en.json": {Data: []byte(`{`)}},
		"non-string":   {"locales/en.json": {Data: []byte(`{"a": 1}`)}},
	} {
		if _, err := Load(fsys); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
