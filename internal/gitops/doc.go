// Package gitops is configuration as code (docs/spec/09-config-as-code.md): YAML documents for
// connections, channels, queries and reports, parsed with file and line positions, exported
// deterministically, planned against a workspace (create, unchanged, conflict) and applied in one
// transaction through the same services as the API. Secrets never leave as values: exports write
// ${env:NAME} placeholders, which imports resolve from the environment or from the user.
package gitops

import (
	"maps"
	"slices"

	"go.yaml.in/yaml/v3"
)

// APIVersion is the only document version this release reads and writes.
const APIVersion = "rowbird.dev/v1"

// Kinds, in the order they are applied (dependencies first).
const (
	KindConnection = "Connection"
	KindChannel    = "Channel"
	KindQuery      = "Query"
	KindReport     = "Report"
)

// Kinds lists the kinds in application order.
var Kinds = []string{KindConnection, KindChannel, KindQuery, KindReport}

// Document is one YAML document. Exactly one spec matches Kind.
type Document struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`

	Connection *ConnectionSpec `yaml:"-"`
	Channel    *ChannelSpec    `yaml:"-"`
	Query      *QuerySpec      `yaml:"-"`
	Report     *ReportSpec     `yaml:"-"`

	// Pos is where the document starts.
	Pos Pos `yaml:"-"`
}

// Metadata names a resource. Name is the reference used by other documents: a connection's or
// channel's name, a query's or report's slug.
type Metadata struct {
	Name        string `yaml:"name"`
	Title       string `yaml:"title,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// ConnectionSpec is a database connection. Secret fields of the driver's schema go in Secrets.
type ConnectionSpec struct {
	Driver  string             `yaml:"driver"`
	Config  map[string]any     `yaml:"config,omitempty"`
	Secrets map[string]any     `yaml:"secrets,omitempty"`
	Options *ConnectionOptions `yaml:"options,omitempty"`
}

// ConnectionOptions are a connection's limits and AI settings.
type ConnectionOptions struct {
	QueryTimeoutSeconds *int     `yaml:"queryTimeoutSeconds,omitempty"`
	MaxRows             *int     `yaml:"maxRows,omitempty"`
	AllowMultiStatement *bool    `yaml:"allowMultiStatement,omitempty"`
	AIExcludedTables    []string `yaml:"aiExcludedTables,omitempty"`
}

// ChannelSpec is a delivery channel. Secret fields of the destination's schema go in Secrets.
type ChannelSpec struct {
	Type         string         `yaml:"type"`
	Config       map[string]any `yaml:"config,omitempty"`
	Secrets      map[string]any `yaml:"secrets,omitempty"`
	SystemMailer bool           `yaml:"systemMailer,omitempty"`
}

// QuerySpec is a query; its current version is what the document describes.
type QuerySpec struct {
	Connection string  `yaml:"connection"`
	SQL        string  `yaml:"sql"`
	Params     []Param `yaml:"params,omitempty"`
}

// Param is a query parameter definition.
type Param struct {
	Name    string  `yaml:"name"`
	Type    string  `yaml:"type"`
	Default *string `yaml:"default,omitempty"`
}

// ReportSpec is a report with its schedule, condition and deliveries.
type ReportSpec struct {
	Query      ReportQuery       `yaml:"query"`
	Schedule   Schedule          `yaml:"schedule"`
	Condition  *Condition        `yaml:"condition,omitempty"`
	Params     map[string]string `yaml:"params,omitempty"`
	Enabled    *bool             `yaml:"enabled,omitempty"`
	Run        *RunPolicy        `yaml:"run,omitempty"`
	Deliveries []Delivery        `yaml:"deliveries,omitempty"`
}

// ReportQuery references a query by slug (Ref), or holds one inline, which becomes a query with
// the report's slug.
type ReportQuery struct {
	Ref        string  `yaml:"ref,omitempty"`
	Connection string  `yaml:"connection,omitempty"`
	SQL        string  `yaml:"sql,omitempty"`
	Params     []Param `yaml:"params,omitempty"`
}

// Inline reports whether the query is written in the report.
func (q ReportQuery) Inline() bool { return q.Ref == "" }

// Schedule is a cron expression in a time zone.
type Schedule struct {
	Cron     string `yaml:"cron"`
	Timezone string `yaml:"timezone"`
}

// Condition gates delivery; rules are flat: {type: ..., <params>}.
type Condition struct {
	Match string `yaml:"match,omitempty"`
	Rules []Rule `yaml:"rules,omitempty"`
}

// Rule is a condition rule: its type and its parameters side by side.
type Rule map[string]any

// MarshalYAML writes the type first, then the parameters in key order.
func (r Rule) MarshalYAML() (any, error) {
	n := &yaml.Node{Kind: yaml.MappingNode}
	add := func(k string, v any) error {
		var val yaml.Node
		if err := val.Encode(v); err != nil {
			return err
		}
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &val)
		return nil
	}
	if t, ok := r["type"]; ok {
		if err := add("type", t); err != nil {
			return nil, err
		}
	}
	for _, k := range slices.Sorted(maps.Keys(r)) {
		if k != "type" {
			if err := add(k, r[k]); err != nil {
				return nil, err
			}
		}
	}
	return n, nil
}

// RunPolicy holds how a report runs: limits, retries and pausing.
type RunPolicy struct {
	MaxRows             *int    `yaml:"maxRows,omitempty"`
	RetryMax            *int    `yaml:"retryMax,omitempty"`
	RetryBackoffSeconds *int    `yaml:"retryBackoffSeconds,omitempty"`
	Misfire             *string `yaml:"misfire,omitempty"`
	Overlap             *string `yaml:"overlap,omitempty"`
	AutoPauseAfter      *int    `yaml:"autoPauseAfter,omitempty"`
	NotifyOwner         *bool   `yaml:"notifyOwner,omitempty"`
}

// Delivery sends a report's runs to a channel.
type Delivery struct {
	Channel string         `yaml:"channel"`
	Mode    string         `yaml:"mode"`
	Formats []string       `yaml:"formats,omitempty"`
	Enabled *bool          `yaml:"enabled,omitempty"`
	Inline  *InlineOptions `yaml:"inline,omitempty"`
	Link    *LinkOptions   `yaml:"link,omitempty"`
	Options map[string]any `yaml:"options,omitempty"`
}

// InlineOptions control the rows shown in a message.
type InlineOptions struct {
	Rows      *int  `yaml:"rows,omitempty"`
	WithFiles *bool `yaml:"withFiles,omitempty"`
}

// LinkOptions control shared links. ExpiresIn is a duration such as 30d, 12h or 90m.
type LinkOptions struct {
	ExpiresIn    string `yaml:"expiresIn,omitempty"`
	RequireLogin *bool  `yaml:"requireLogin,omitempty"`
}

// Key identifies a document within a set.
func (d *Document) Key() string { return d.Kind + "/" + d.Metadata.Name }
