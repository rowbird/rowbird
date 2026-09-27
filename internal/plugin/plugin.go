// Package plugin holds the contracts shared by every plugin kind and the registry that maps plugin
// ids to implementations (ADR-0004, docs/spec/04-plugins.md). The core depends only on this package,
// never on concrete plugins.
package plugin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
)

// Kind is a plugin kind.
type Kind string

// Plugin kinds (docs/spec/01-architecture.md).
const (
	KindConnector   Kind = "connector"
	KindFormatter   Kind = "formatter"
	KindCondition   Kind = "condition"
	KindDestination Kind = "destination"
	KindAIProvider  Kind = "ai_provider"
	KindStorage     Kind = "storage"
)

// Metadata describes a plugin. Name and Description are i18n keys resolved with Messages.
type Metadata struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Version     string `json:"version"`
}

// Messages maps locale to translation key to text. Plugins ship their own translations so that
// adding one never requires a frontend change.
type Messages map[string]map[string]string

// Plugin is implemented by every plugin.
type Plugin interface {
	Meta() Metadata
	ConfigSchema() *Schema
	// Capabilities returns a kind-specific struct, serialized as JSON for the API.
	Capabilities() any
	Messages() Messages
}

// Factory builds a plugin. Plugins are stateless; the registry keeps one instance per id.
type Factory func() Plugin

var (
	mu       sync.RWMutex
	registry = map[Kind]map[string]Plugin{}
)

// Register adds a plugin; it is called from the plugin package's init. Registering the same id
// twice, or a plugin whose metadata disagrees with id, is a programming error and panics.
func Register(kind Kind, id string, factory Factory) {
	p := factory()
	if p.Meta().ID != id {
		panic(fmt.Sprintf("plugin: %s %q reports id %q", kind, id, p.Meta().ID))
	}
	mu.Lock()
	defer mu.Unlock()
	if registry[kind] == nil {
		registry[kind] = map[string]Plugin{}
	}
	if _, dup := registry[kind][id]; dup {
		panic(fmt.Sprintf("plugin: %s %q registered twice", kind, id))
	}
	registry[kind][id] = p
}

// Get returns the plugin of kind with id.
func Get(kind Kind, id string) (Plugin, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[kind][id]
	return p, ok
}

// List returns the plugins of kind, sorted by id.
func List(kind Kind) []Plugin {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Plugin, 0, len(registry[kind]))
	for _, p := range registry[kind] {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Plugin) int { return strings.Compare(a.Meta().ID, b.Meta().ID) })
	return out
}

// Kinds returns the kinds that have at least one plugin, sorted.
func Kinds() []Kind {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Kind, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// unregister is for tests that register throwaway plugins.
func unregister(kind Kind, id string) {
	mu.Lock()
	defer mu.Unlock()
	delete(registry[kind], id)
	if len(registry[kind]) == 0 {
		delete(registry, kind)
	}
}

// MergeMessages combines message catalogs; later catalogs win on conflicts.
func MergeMessages(catalogs ...Messages) Messages {
	out := Messages{}
	for _, c := range catalogs {
		for locale, msgs := range c {
			if out[locale] == nil {
				out[locale] = map[string]string{}
			}
			for k, v := range msgs {
				out[locale][k] = v
			}
		}
	}
	return out
}

// MustLoadMessages reads locales/<locale>.json files (flat key to text maps) from fsys. Plugins
// embed their catalogs; a malformed catalog is a build defect and panics at startup.
func MustLoadMessages(fsys fs.FS) Messages {
	files, err := fs.Glob(fsys, "locales/*.json")
	if err != nil || len(files) == 0 {
		panic(fmt.Sprintf("plugin: no locales/*.json catalogs: %v", err))
	}
	out := Messages{}
	for _, f := range files {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			panic(err)
		}
		var msgs map[string]string
		if err := json.Unmarshal(data, &msgs); err != nil {
			panic(fmt.Sprintf("plugin: %s: %v", f, err))
		}
		out[strings.TrimSuffix(path.Base(f), ".json")] = msgs
	}
	return out
}
