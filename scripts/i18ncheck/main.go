// Command i18ncheck verifies that every locale file has exactly the keys of the English file, for
// the web UI and for the server catalogs (ADR-0014). It exits 1 and lists the differences otherwise.
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rowbird/rowbird/internal/i18n"
)

// catalogDirs returns the web catalogs plus every locales directory under internal/, which covers
// the server catalogs and the catalogs embedded in each plugin.
func catalogDirs() ([]string, error) {
	dirs := []string{"web/src/locales"}
	err := filepath.WalkDir("internal", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "locales" {
			dirs = append(dirs, p)
		}
		return nil
	})
	return dirs, err
}

func main() {
	dirs, err := catalogDirs()
	if err != nil {
		fmt.Fprintln(os.Stderr, "i18ncheck:", err)
		os.Exit(1)
	}
	var problems []string
	for _, dir := range dirs {
		p, err := checkDir(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "i18ncheck:", err)
			os.Exit(1)
		}
		problems = append(problems, p...)
	}
	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "i18n catalogs are incomplete:")
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "  "+p)
		}
		os.Exit(1)
	}
	fmt.Printf("i18n catalogs are complete (%d directories)\n", len(dirs))
}

func checkDir(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	catalogs := map[string]map[string]string{}
	for _, f := range files {
		keys, err := load(f)
		if err != nil {
			return nil, err
		}
		catalogs[strings.TrimSuffix(filepath.Base(f), ".json")] = keys
	}
	base, ok := catalogs[i18n.DefaultLocale]
	if !ok {
		return nil, fmt.Errorf("%s has no %s.json", dir, i18n.DefaultLocale)
	}
	if len(catalogs) < 2 {
		return nil, fmt.Errorf("%s has only one locale", dir)
	}

	var problems []string
	for locale, keys := range catalogs {
		for k := range base {
			if _, ok := keys[k]; !ok {
				problems = append(problems, fmt.Sprintf("%s/%s.json: missing %s", dir, locale, k))
			}
		}
		for k, v := range keys {
			if _, ok := base[k]; !ok {
				problems = append(problems, fmt.Sprintf("%s/%s.json: unknown key %s (not in %s)", dir, locale, k, i18n.DefaultLocale))
			}
			if strings.TrimSpace(v) == "" {
				problems = append(problems, fmt.Sprintf("%s/%s.json: empty message for %s", dir, locale, k))
			}
		}
	}
	slices.Sort(problems)
	return problems, nil
}

func load(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // paths come from a fixed list of repository directories
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := map[string]string{}
	if err := i18n.Flatten("", tree, out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}
