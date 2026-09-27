package gitops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"

	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/condition"
	"github.com/rowbird/rowbird/internal/connections"
	"github.com/rowbird/rowbird/internal/store"
)

// Selection says what to export. No reports and no queries means the whole workspace.
type Selection struct {
	// Reports and Queries are slugs; a report brings its query.
	Reports []string
	Queries []string
	// IncludeConnections and IncludeChannels add the connections and channels the selection uses
	// (all of them for a whole-workspace export).
	IncludeConnections bool
	IncludeChannels    bool
}

func (s Selection) all() bool { return len(s.Reports) == 0 && len(s.Queries) == 0 }

// Exporter writes a workspace's resources as documents.
type Exporter struct {
	store *store.Store
	conns *connections.Service
	chans *channels.Service
}

// NewExporter builds an exporter. The services tell which secrets are set, without decrypting them.
func NewExporter(st *store.Store, conns *connections.Service, chans *channels.Service) *Exporter {
	return &Exporter{store: st, conns: conns, chans: chans}
}

// ErrUnknownSelection is a selected slug that does not exist.
type ErrUnknownSelection struct{ Kind, Name string }

func (e *ErrUnknownSelection) Error() string {
	return fmt.Sprintf("%s %q does not exist", e.Kind, e.Name)
}

// Export returns the selected documents in a stable order.
func (e *Exporter) Export(ctx context.Context, sel Selection) ([]*Document, error) {
	reports, err := e.store.Reports().List(ctx)
	if err != nil {
		return nil, err
	}
	queries, err := e.store.Queries().List(ctx)
	if err != nil {
		return nil, err
	}
	queryByID := map[uuid.UUID]store.Query{}
	for _, q := range queries {
		queryByID[q.ID] = q
	}
	pickReports := map[uuid.UUID]bool{}
	pickQueries := map[uuid.UUID]bool{}
	for _, slug := range sel.Reports {
		i := slices.IndexFunc(reports, func(r store.Report) bool { return r.Slug == slug })
		if i < 0 {
			return nil, &ErrUnknownSelection{KindReport, slug}
		}
		pickReports[reports[i].ID] = true
		pickQueries[reports[i].QueryID] = true
	}
	for _, slug := range sel.Queries {
		i := slices.IndexFunc(queries, func(q store.Query) bool { return q.Slug == slug })
		if i < 0 {
			return nil, &ErrUnknownSelection{KindQuery, slug}
		}
		pickQueries[queries[i].ID] = true
	}
	if sel.all() {
		for _, r := range reports {
			pickReports[r.ID] = true
		}
		for _, q := range queries {
			pickQueries[q.ID] = true
		}
	}

	var docs []*Document
	usedConns := map[uuid.UUID]bool{}
	usedChans := map[uuid.UUID]bool{}
	conns, err := e.conns.List(ctx)
	if err != nil {
		return nil, err
	}
	connName := map[uuid.UUID]string{}
	for _, c := range conns {
		connName[c.ID] = c.Name
	}
	chans, err := e.chans.List(ctx)
	if err != nil {
		return nil, err
	}
	chanName := map[uuid.UUID]string{}
	for _, c := range chans {
		chanName[c.ID] = c.Name
	}

	for _, q := range queries {
		if !pickQueries[q.ID] {
			continue
		}
		if q.CurrentVersionID == nil {
			continue
		}
		v, err := e.store.Queries().VersionByID(ctx, *q.CurrentVersionID)
		if err != nil {
			return nil, err
		}
		usedConns[q.ConnectionID] = true
		spec := &QuerySpec{Connection: connName[q.ConnectionID], SQL: v.SQL}
		for _, p := range v.Params {
			spec.Params = append(spec.Params, Param{Name: p.Name, Type: p.Type, Default: p.Default})
		}
		docs = append(docs, &Document{Kind: KindQuery, Metadata: Metadata{Name: q.Slug, Title: q.Title, Description: q.Description}, Query: spec})
	}
	for _, r := range reports {
		if !pickReports[r.ID] {
			continue
		}
		ds, err := e.store.Deliveries().ListByReport(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range ds {
			usedChans[d.ChannelID] = true
		}
		docs = append(docs, reportDocument(r, queryByID[r.QueryID].Slug, ds, chanName))
	}
	for _, c := range conns {
		if sel.IncludeConnections && (sel.all() || usedConns[c.ID]) {
			docs = append(docs, connectionDocument(c))
		}
	}
	for _, c := range chans {
		if sel.IncludeChannels && (sel.all() || usedChans[c.ID]) {
			docs = append(docs, channelDocument(c))
		}
	}
	sortDocuments(docs)
	return docs, nil
}

func sortDocuments(docs []*Document) {
	slices.SortStableFunc(docs, func(a, b *Document) int {
		if ka, kb := slices.Index(Kinds, a.Kind), slices.Index(Kinds, b.Kind); ka != kb {
			return ka - kb
		}
		return strings.Compare(a.Metadata.Name, b.Metadata.Name)
	})
	for _, d := range docs {
		d.APIVersion = APIVersion
	}
}

var envUnsafe = regexp.MustCompile(`[^A-Z0-9]+`)

// SecretEnv is the environment variable an export suggests for a secret field.
func SecretEnv(kind, name, field string) string {
	up := func(s string) string { return strings.Trim(envUnsafe.ReplaceAllString(strings.ToUpper(s), "_"), "_") }
	return "ROWBIRD_" + up(kind) + "_" + up(name) + "_" + up(field)
}

func secretPlaceholders(kind, name string, configured map[string]bool) map[string]any {
	var out map[string]any
	for field, set := range configured {
		if !set {
			continue
		}
		if out == nil {
			out = map[string]any{}
		}
		out[field] = Placeholder(SecretEnv(kind, name, field))
	}
	return out
}

func nonEmpty(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	return m
}

func connectionDocument(c connections.View) *Document {
	spec := &ConnectionSpec{
		Driver: c.Driver, Config: nonEmpty(c.Config), Secrets: secretPlaceholders(KindConnection, c.Name, c.SecretsConfigured),
		Options: &ConnectionOptions{
			QueryTimeoutSeconds: &c.QueryTimeoutSeconds, MaxRows: &c.MaxRows, AllowMultiStatement: &c.AllowMultiStatement,
			AIExcludedTables: slices.Sorted(slices.Values(c.AIExcludedTables)),
		},
	}
	if len(spec.Options.AIExcludedTables) == 0 {
		spec.Options.AIExcludedTables = nil
	}
	return &Document{Kind: KindConnection, Metadata: Metadata{Name: c.Name}, Connection: spec}
}

func channelDocument(c channels.View) *Document {
	return &Document{Kind: KindChannel, Metadata: Metadata{Name: c.Name}, Channel: &ChannelSpec{
		Type: c.Type, Config: nonEmpty(c.Config), Secrets: secretPlaceholders(KindChannel, c.Name, c.SecretsConfigured), SystemMailer: c.IsSystemMailer,
	}}
}

func reportDocument(r store.Report, querySlug string, ds []store.Delivery, chanName map[uuid.UUID]string) *Document {
	enabled := r.Enabled
	misfire, overlap := r.MisfirePolicy, r.OverlapPolicy
	retry, backoff, pause, notify := r.RetryMax, r.RetryBackoffSeconds, r.AutoPauseAfter, r.NotifyOwnerOnFail
	spec := &ReportSpec{
		Query:    ReportQuery{Ref: querySlug},
		Schedule: Schedule{Cron: r.Cron, Timezone: r.Timezone},
		Enabled:  &enabled,
		Run: &RunPolicy{
			MaxRows: r.MaxRows, RetryMax: &retry, RetryBackoffSeconds: &backoff, Misfire: &misfire, Overlap: &overlap,
			AutoPauseAfter: &pause, NotifyOwner: &notify,
		},
	}
	if len(r.ParamOverrides) > 0 {
		spec.Params = r.ParamOverrides
	}
	var cond condition.Spec
	if json.Unmarshal(r.Condition, &cond) == nil && len(cond.Rules) > 0 {
		spec.Condition = conditionDoc(cond)
	}
	for _, d := range ds {
		dEnabled, rows, withFiles, login := d.Enabled, d.InlineRowLimit, d.IncludeInlineWithFiles, d.LinkRequireLogin
		spec.Deliveries = append(spec.Deliveries, Delivery{
			Channel: chanName[d.ChannelID], Mode: d.Mode, Formats: d.Formats, Enabled: &dEnabled,
			Inline:  &InlineOptions{Rows: &rows, WithFiles: &withFiles},
			Link:    &LinkOptions{ExpiresIn: FormatDuration(d.LinkExpiresSeconds), RequireLogin: &login},
			Options: nonEmpty(d.Options),
		})
	}
	return &Document{Kind: KindReport, Metadata: Metadata{Name: r.Slug, Title: r.Title, Description: r.Description}, Report: spec}
}

// envelope is how a document is written: the spec of its kind under "spec".
type envelope struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       any      `yaml:"spec"`
}

// Encode writes documents separated by ---. Map keys come out sorted, so the output is stable.
func Encode(docs []*Document) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, d := range docs {
		var spec any
		switch d.Kind {
		case KindConnection:
			spec = d.Connection
		case KindChannel:
			spec = d.Channel
		case KindQuery:
			spec = d.Query
		case KindReport:
			spec = d.Report
		}
		if err := enc.Encode(envelope{APIVersion: APIVersion, Kind: d.Kind, Metadata: d.Metadata, Spec: spec}); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// plain turns json.Number (how stored JSON keeps numbers exact) into int64 or float64, so YAML
// writes numbers rather than quoted strings.
func plain(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = plain(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = plain(e)
		}
		return out
	}
	return v
}
