// Package i18n holds the server-side message catalogs (ADR-0014). Catalogs are nested JSON files in
// locales/, one per language; keys are addressed with dots, for example "errors.internal".
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"golang.org/x/text/language"
)

// DefaultLocale is used when nothing better matches, and as the fallback for missing keys.
const DefaultLocale = "en"

//go:embed locales/*.json
var localesFS embed.FS

// Bundle is a set of flattened catalogs keyed by locale.
type Bundle struct {
	catalogs map[string]map[string]string
	tags     []language.Tag
	names    []string
	matcher  language.Matcher
}

var defaultBundle = mustLoad(localesFS)

// Default returns the bundle built from the embedded catalogs.
func Default() *Bundle { return defaultBundle }

func mustLoad(fsys fs.FS) *Bundle {
	b, err := Load(fsys)
	if err != nil {
		panic(err)
	}
	return b
}

// Load reads every locales/*.json file in fsys.
func Load(fsys fs.FS) (*Bundle, error) {
	files, err := fs.Glob(fsys, "locales/*.json")
	if err != nil {
		return nil, err
	}
	b := &Bundle{catalogs: map[string]map[string]string{}}
	// The default locale goes first so the matcher falls back to it.
	for _, f := range files {
		name := strings.TrimSuffix(path.Base(f), ".json")
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		var tree map[string]any
		if err := json.Unmarshal(data, &tree); err != nil {
			return nil, fmt.Errorf("i18n: parse %s: %w", f, err)
		}
		flat := map[string]string{}
		if err := Flatten("", tree, flat); err != nil {
			return nil, fmt.Errorf("i18n: %s: %w", f, err)
		}
		b.catalogs[name] = flat
		tag := language.MustParse(name)
		if name == DefaultLocale {
			b.tags = append([]language.Tag{tag}, b.tags...)
			b.names = append([]string{name}, b.names...)
		} else {
			b.tags = append(b.tags, tag)
			b.names = append(b.names, name)
		}
	}
	if _, ok := b.catalogs[DefaultLocale]; !ok {
		return nil, fmt.Errorf("i18n: missing %s catalog", DefaultLocale)
	}
	b.matcher = language.NewMatcher(b.tags)
	return b, nil
}

// Flatten turns a nested catalog into dotted keys. Values must be strings.
func Flatten(prefix string, tree map[string]any, out map[string]string) error {
	for k, v := range tree {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case string:
			out[key] = val
		case map[string]any:
			if err := Flatten(key, val, out); err != nil {
				return err
			}
		default:
			return fmt.Errorf("key %s: value must be a string or an object", key)
		}
	}
	return nil
}

// Locales lists the available locale names, default first.
func (b *Bundle) Locales() []string { return append([]string(nil), b.names...) }

// Keys returns the keys of one locale's catalog.
func (b *Bundle) Keys(locale string) map[string]string { return b.catalogs[locale] }

// Match picks the best available locale for an Accept-Language header value.
func (b *Bundle) Match(acceptLanguage string) string {
	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(tags) == 0 {
		return DefaultLocale
	}
	_, idx, conf := b.matcher.Match(tags...)
	if conf == language.No {
		return DefaultLocale
	}
	return b.names[idx]
}

// T returns the message for key in locale, falling back to the default locale and then to the key
// itself, so a missing translation is visible but never breaks a response.
func (b *Bundle) T(locale, key string) string {
	if msg, ok := b.catalogs[locale][key]; ok {
		return msg
	}
	if msg, ok := b.catalogs[DefaultLocale][key]; ok {
		return msg
	}
	return key
}
