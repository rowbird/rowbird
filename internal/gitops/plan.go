package gitops

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/condition"
	"github.com/rowbird/rowbird/internal/connections"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/reports"
	"github.com/rowbird/rowbird/internal/store"
)

// Policy decides what happens to a document whose resource exists and differs.
type Policy string

// Conflict policies (docs/spec/09-config-as-code.md).
const (
	PolicyFail      Policy = "fail"
	PolicyOverwrite Policy = "overwrite"
	PolicyCopy      Policy = "copy"
	PolicySkip      Policy = "skip"
)

// ValidPolicy reports whether p is a policy.
func ValidPolicy(p Policy) bool {
	return p == PolicyFail || p == PolicyOverwrite || p == PolicyCopy || p == PolicySkip
}

// Action is what applying a document does.
type Action string

// Actions of a plan item.
const (
	ActionCreate    Action = "create"
	ActionUpdate    Action = "update"
	ActionUnchanged Action = "unchanged"
	ActionConflict  Action = "conflict"
	ActionSkip      Action = "skip"
)

// Options tune a plan.
type Options struct {
	// Policy applies to conflicts without their own; empty means fail.
	Policy Policy
	// Items sets the policy of single documents, by key ("Report/daily-sales").
	Items map[string]Policy
	// Map points references to resources the documents do not have and the workspace lacks:
	// old name to existing name, for connections and channels.
	Map map[string]string
	// Secrets are values for ${env:NAME} placeholders given by the user; Env looks up the rest.
	Secrets map[string]string
	Env     func(name string) (string, bool)
}

// Change is a field that differs. Secret changes carry no values.
type Change struct {
	Field  string `json:"field"`
	From   any    `json:"from,omitempty"`
	To     any    `json:"to,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

// Item is the plan for one document.
type Item struct {
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Pos     Pos      `json:"pos"`
	Action  Action   `json:"action"`
	Policy  Policy   `json:"policy,omitempty"`
	Target  string   `json:"target"`
	Changes []Change `json:"changes"`
}

// Key is the item's document key.
func (i Item) Key() string { return i.Kind + "/" + i.Name }

// Missing is a reference to a resource neither the documents nor the workspace have.
type Missing struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Pos  Pos    `json:"pos"`
}

// SecretNeed is a ${env:NAME} placeholder: where it goes, whether a value was found, and whether
// one is required (a new resource) or optional (an existing one keeps its stored secret).
type SecretNeed struct {
	Env      string `json:"env"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Field    string `json:"field"`
	Required bool   `json:"required"`
	Provided bool   `json:"provided"`
}

// Plan is what applying a set of documents would do.
type Plan struct {
	Items   []Item       `json:"items"`
	Missing []Missing    `json:"missing"`
	Secrets []SecretNeed `json:"secrets"`
	Errors  Errors       `json:"errors"`

	docs     []*Document
	resolved map[string]*resolved
}

// Blocked reports whether the plan cannot be applied as is: invalid documents, missing
// references or required secrets, or conflicts left to fail.
func (p *Plan) Blocked() bool {
	if len(p.Errors) > 0 || len(p.Missing) > 0 {
		return true
	}
	for _, s := range p.Secrets {
		if s.Required && !s.Provided {
			return true
		}
	}
	return slices.ContainsFunc(p.Items, func(i Item) bool { return i.Action == ActionConflict })
}

// resolved is a document ready to apply: references as the names they will have, secrets
// resolved, values validated.
type resolved struct {
	doc *Document
	// config is the plain configuration, secrets the values found (connections and channels).
	config  map[string]any
	secrets map[string]any
	// connection, query and channels are resolved reference names.
	connection string
	query      string
	channels   []string
	cond       *condition.Spec
	options    []map[string]any
}

// Planner compares documents with a workspace.
type Planner struct {
	store    *store.Store
	conns    *connections.Service
	chans    *channels.Service
	queries  *queries.Service
	reports  *reports.Service
	exporter *Exporter
}

// NewPlanner builds a planner on the services the API uses, so imports follow the same rules.
func NewPlanner(st *store.Store, conns *connections.Service, chans *channels.Service, qs *queries.Service, rps *reports.Service) *Planner {
	return &Planner{store: st, conns: conns, chans: chans, queries: qs, reports: rps, exporter: NewExporter(st, conns, chans)}
}

// Plan compares docs with the workspace in ctx.
func (pl *Planner) Plan(ctx context.Context, docs []*Document, opts Options) (*Plan, error) {
	if opts.Policy == "" {
		opts.Policy = PolicyFail
	}
	plan := &Plan{Items: []Item{}, Missing: []Missing{}, Secrets: []SecretNeed{}, Errors: Errors{}, resolved: map[string]*resolved{}}
	docs, errs := expandInline(docs)
	plan.Errors = append(plan.Errors, errs...)
	sortDocuments(docs)
	plan.docs = docs

	current, err := pl.exporter.Export(ctx, Selection{IncludeConnections: true, IncludeChannels: true})
	if err != nil {
		return nil, err
	}
	existing := map[string]*Document{}
	for _, d := range current {
		existing[d.Key()] = d
	}
	inSet := map[string]*Document{}
	for _, d := range docs {
		inSet[d.Key()] = d
	}

	// Documents come sorted by kind, dependencies first, so each one is settled after the ones it
	// references: a copy renames a resource, and references to it follow the new name.
	target := map[string]string{}
	ref := func(kind, name string, pos Pos) string {
		key := kind + "/" + name
		if t, ok := target[key]; ok {
			return t
		}
		if _, ok := inSet[key]; ok {
			return name
		}
		if _, ok := existing[key]; ok {
			return name
		}
		if mapped, ok := opts.Map[name]; ok {
			if _, ok := existing[kind+"/"+mapped]; ok {
				return mapped
			}
			if _, ok := inSet[kind+"/"+mapped]; ok {
				return mapped
			}
		}
		if !slices.ContainsFunc(plan.Missing, func(m Missing) bool { return m.Kind == kind && m.Name == name }) {
			plan.Missing = append(plan.Missing, Missing{Kind: kind, Name: name, Pos: pos})
		}
		return name
	}

	for _, d := range docs {
		it := Item{Kind: d.Kind, Name: d.Metadata.Name, Pos: d.Pos, Action: ActionCreate, Target: d.Metadata.Name, Changes: []Change{}}
		r, errs := pl.resolve(ctx, d, existing, inSet, ref, opts, plan)
		plan.Errors = append(plan.Errors, errs...)
		if have, ok := existing[d.Key()]; ok && r != nil {
			it.Policy = opts.Policy
			if p, ok := opts.Items[it.Key()]; ok {
				it.Policy = p
			}
			it.Changes = pl.diff(ctx, have, r)
			switch {
			case len(it.Changes) == 0:
				it.Action, it.Policy = ActionUnchanged, ""
			case it.Policy == PolicyOverwrite:
				it.Action = ActionUpdate
			case it.Policy == PolicyCopy:
				it.Action, it.Target = ActionCreate, freeName(it.Kind, it.Name, existing, inSet, target)
			case it.Policy == PolicySkip:
				it.Action = ActionSkip
			default:
				it.Action = ActionConflict
			}
		} else if ok {
			it.Action = ActionConflict
		}
		target[it.Key()] = it.Target
		if r != nil {
			plan.resolved[d.Key()] = r
		}
		plan.Items = append(plan.Items, it)
	}
	return plan, nil
}

// expandInline turns a report's inline query into a Query document with the report's slug.
func expandInline(docs []*Document) ([]*Document, Errors) {
	var errs Errors
	out := slices.Clone(docs)
	names := map[string]bool{}
	for _, d := range docs {
		names[d.Key()] = true
	}
	for _, d := range docs {
		if d.Kind != KindReport || !d.Report.Query.Inline() {
			continue
		}
		key := KindQuery + "/" + d.Metadata.Name
		if names[key] {
			errs = append(errs, Error{Pos: d.Pos, Message: fmt.Sprintf("the inline query would be named %q, which a Query document already uses; reference it with ref", d.Metadata.Name)})
			continue
		}
		q := d.Report.Query
		out = append(out, &Document{
			APIVersion: APIVersion, Kind: KindQuery, Pos: d.Pos,
			Metadata: Metadata{Name: d.Metadata.Name, Title: d.Metadata.Title, Description: d.Metadata.Description},
			Query:    &QuerySpec{Connection: q.Connection, SQL: q.SQL, Params: q.Params},
		})
		spec := *d.Report
		spec.Query = ReportQuery{Ref: d.Metadata.Name}
		d.Report = &spec
		names[key] = true
	}
	return out, errs
}

// freeName finds name-2, name-3, ... unused by the workspace and the documents.
func freeName(kind, name string, existing, inSet map[string]*Document, taken map[string]string) string {
	used := func(n string) bool {
		if _, ok := existing[kind+"/"+n]; ok {
			return true
		}
		if _, ok := inSet[kind+"/"+n]; ok {
			return true
		}
		for k, t := range taken {
			if strings.HasPrefix(k, kind+"/") && t == n {
				return true
			}
		}
		return false
	}
	for i := 2; ; i++ {
		base := name
		if len(base) > 58 {
			base = base[:58]
		}
		if n := fmt.Sprintf("%s-%d", base, i); !used(n) {
			return n
		}
	}
}

// resolve validates a document and resolves its references and secrets.
func (pl *Planner) resolve(ctx context.Context, d *Document, existing, inSet map[string]*Document, ref func(kind, name string, pos Pos) string, opts Options, plan *Plan) (*resolved, Errors) {
	r := &resolved{doc: d}
	var errs Errors
	fail := func(format string, args ...any) {
		errs = append(errs, Error{Pos: d.Pos, Message: fmt.Sprintf(format, args...)})
	}
	switch d.Kind {
	case KindConnection, KindChannel:
		var schema *plugin.Schema
		var config, secrets map[string]any
		var id func() (map[string]any, error)
		if d.Kind == KindConnection {
			p, ok := plugin.Get(plugin.KindConnector, d.Connection.Driver)
			if !ok {
				fail("unknown driver %q", d.Connection.Driver)
				return nil, errs
			}
			schema, config, secrets = p.ConfigSchema(), d.Connection.Config, d.Connection.Secrets
			if e, ok := existing[d.Key()]; ok && e != nil {
				id = func() (map[string]any, error) { return pl.connectionValues(ctx, d.Metadata.Name) }
			}
		} else {
			dest, ok := channels.Destination(d.Channel.Type)
			if !ok {
				fail("unknown channel type %q", d.Channel.Type)
				return nil, errs
			}
			schema, config, secrets = dest.ConfigSchema(), d.Channel.Config, d.Channel.Secrets
			if _, ok := existing[d.Key()]; ok {
				id = func() (map[string]any, error) { return pl.channelValues(ctx, d.Metadata.Name) }
			}
		}
		values := map[string]any{}
		for k, v := range config {
			values[k] = v
		}
		found := map[string]any{}
		var stored map[string]any
		if id != nil {
			var err error
			if stored, err = id(); err != nil {
				fail("%v", err)
				return nil, errs
			}
		}
		for field, v := range secrets {
			env, ok := EnvName(v)
			if !ok {
				found[field] = v
				continue
			}
			need := SecretNeed{Env: env, Kind: d.Kind, Name: d.Metadata.Name, Field: field, Required: id == nil}
			if val, ok := opts.Secrets[env]; ok && val != "" {
				found[field], need.Provided = val, true
			} else if opts.Env != nil {
				if val, ok := opts.Env(env); ok && val != "" {
					found[field], need.Provided = val, true
				}
			}
			plan.Secrets = append(plan.Secrets, need)
		}
		// Validate with every secret the resource will have: found, stored, or a stand-in for
		// one still to be given (reported as a secret need, not as a validation error).
		for _, k := range schema.SecretKeys() {
			switch {
			case found[k] != nil:
				values[k] = found[k]
			case stored[k] != nil:
				values[k] = stored[k]
			case secrets[k] != nil:
				values[k] = "unresolved"
			}
		}
		for k, v := range config {
			if f, ok := schema.Field(k); ok && f.Secret {
				found[k] = v
			}
		}
		validated, err := schema.Validate(values)
		if err != nil {
			for _, e := range fieldErrors(d.Pos, "spec.config.", err) {
				// Secret fields are written under spec.secrets.
				for _, k := range schema.SecretKeys() {
					if strings.HasPrefix(e.Message, "spec.config."+k+":") {
						e.Message = "spec.secrets." + strings.TrimPrefix(e.Message, "spec.config.")
					}
				}
				errs = append(errs, e)
			}
			return nil, errs
		}
		r.config, _ = schema.Split(validated)
		r.secrets = found
	case KindQuery:
		r.connection = ref(KindConnection, d.Query.Connection, d.Pos)
	case KindReport:
		r.query = ref(KindQuery, d.Report.Query.Ref, d.Pos)
		if c := d.Report.Condition; c != nil {
			spec := condition.Spec{Match: c.Match, Rules: make([]condition.Rule, 0, len(c.Rules))}
			if spec.Match == "" {
				spec.Match = condition.MatchAll
			}
			for _, rule := range c.Rules {
				t, _ := rule["type"].(string)
				params := map[string]any{}
				for k, v := range rule {
					if k != "type" {
						params[k] = v
					}
				}
				spec.Rules = append(spec.Rules, condition.Rule{Type: t, Params: params})
			}
			norm, err := condition.Validate(spec, "spec.condition")
			if err != nil {
				errs = append(errs, fieldErrors(d.Pos, "", err)...)
			} else {
				r.cond = &norm
			}
		}
		for i, dl := range d.Report.Deliveries {
			name := ref(KindChannel, dl.Channel, d.Pos)
			r.channels = append(r.channels, name)
			typ := ""
			if c, ok := inSet[KindChannel+"/"+dl.Channel]; ok {
				typ = c.Channel.Type
			} else if c, ok := existing[KindChannel+"/"+name]; ok {
				typ = c.Channel.Type
			}
			opts := map[string]any{}
			if dest, ok := channels.Destination(typ); ok {
				v, err := dest.DeliverySchema().Validate(orEmpty(dl.Options))
				if err != nil {
					errs = append(errs, fieldErrors(d.Pos, fmt.Sprintf("spec.deliveries[%d].options.", i), err)...)
				} else {
					opts = v
				}
			}
			r.options = append(r.options, opts)
		}
	}
	return r, errs
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func fieldErrors(pos Pos, prefix string, err error) Errors {
	ae, ok := apperr.As(err)
	if !ok || len(ae.Fields) == 0 {
		return Errors{{Pos: pos, Message: err.Error()}}
	}
	out := make(Errors, len(ae.Fields))
	for i, f := range ae.Fields {
		out[i] = Error{Pos: pos, Message: fmt.Sprintf("%s%s: %s", prefix, f.Field, f.Code)}
	}
	return out
}

func (pl *Planner) connectionValues(ctx context.Context, name string) (map[string]any, error) {
	c, err := pl.store.Connections().GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	return pl.conns.Values(ctx, c.ID)
}

func (pl *Planner) channelValues(ctx context.Context, name string) (map[string]any, error) {
	c, err := pl.store.Channels().GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	_, env, _, err := pl.chans.Env(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return env.Config, nil
}

// want is the resolved document as the exporter would write it, for comparison.
func (r *resolved) want() *Document {
	d := *r.doc
	switch d.Kind {
	case KindConnection:
		spec := *d.Connection
		spec.Config, spec.Secrets = nonEmpty(r.config), nil
		d.Connection = &spec
	case KindChannel:
		spec := *d.Channel
		spec.Config, spec.Secrets = nonEmpty(r.config), nil
		d.Channel = &spec
	case KindQuery:
		spec := *d.Query
		spec.Connection, spec.SQL = r.connection, strings.TrimSpace(spec.SQL)
		d.Query = &spec
	case KindReport:
		spec := *d.Report
		spec.Query = ReportQuery{Ref: r.query}
		spec.Condition = nil
		if r.cond != nil && len(r.cond.Rules) > 0 {
			spec.Condition = conditionDoc(*r.cond)
		}
		spec.Deliveries = nil
		for i, dl := range d.Report.Deliveries {
			spec.Deliveries = append(spec.Deliveries, normalizedDelivery(dl, r.channels[i], r.options[i]))
		}
		d.Report = &spec
	}
	return &d
}

// Delivery defaults, as the API applies them.
const (
	defaultInlineRows  = 20
	defaultLinkSeconds = 7 * 86400
)

func normalizedDelivery(dl Delivery, channel string, options map[string]any) Delivery {
	enabled, rows, withFiles, login, expires := true, defaultInlineRows, true, false, defaultLinkSeconds
	if dl.Enabled != nil {
		enabled = *dl.Enabled
	}
	if dl.Inline != nil {
		if dl.Inline.Rows != nil {
			rows = *dl.Inline.Rows
		}
		if dl.Inline.WithFiles != nil {
			withFiles = *dl.Inline.WithFiles
		}
	}
	if dl.Link != nil {
		if dl.Link.RequireLogin != nil {
			login = *dl.Link.RequireLogin
		}
		if s, err := ParseDuration(dl.Link.ExpiresIn); err == nil {
			expires = s
		}
	}
	return Delivery{
		Channel: channel, Mode: dl.Mode, Formats: dl.Formats, Enabled: &enabled,
		Inline:  &InlineOptions{Rows: &rows, WithFiles: &withFiles},
		Link:    &LinkOptions{ExpiresIn: FormatDuration(expires), RequireLogin: &login},
		Options: nonEmpty(options),
	}
}

func conditionDoc(c condition.Spec) *Condition {
	out := &Condition{Match: c.Match}
	for _, rule := range c.Rules {
		flat := Rule{"type": rule.Type}
		for k, v := range rule.Params {
			flat[k] = plain(v)
		}
		out.Rules = append(out.Rules, flat)
	}
	return out
}

// wholeFields are compared as a whole and declared by the document: the services replace them
// entirely, and a document that leaves one out empties it. Other fields a document leaves out
// keep their current value.
var wholeFields = map[string]bool{"spec.config": true, "spec.params": true, "spec.condition": true, "spec.deliveries": true}

// diff lists what applying r would change in have. Fields the document leaves out keep their
// value, so they are not compared; secrets are compared with the stored values without showing
// them.
func (pl *Planner) diff(ctx context.Context, have *Document, r *resolved) []Change {
	want := r.want()
	var changes []Change
	compare(generic(have), generic(want), "", &changes)
	var stored map[string]any
	switch r.doc.Kind {
	case KindConnection:
		stored, _ = pl.connectionValues(ctx, r.doc.Metadata.Name)
	case KindChannel:
		stored, _ = pl.channelValues(ctx, r.doc.Metadata.Name)
	}
	for _, k := range slices.Sorted(maps.Keys(r.secrets)) {
		if !reflect.DeepEqual(stored[k], r.secrets[k]) {
			changes = append(changes, Change{Field: "spec.secrets." + k, Secret: true})
		}
	}
	return changes
}

func generic(d *Document) map[string]any {
	b, _ := Encode([]*Document{d})
	var m map[string]any
	_ = yaml.Unmarshal(b, &m)
	delete(m, "apiVersion")
	delete(m, "kind")
	if md, ok := m["metadata"].(map[string]any); ok {
		delete(md, "name")
	}
	return m
}

func compare(have, want map[string]any, prefix string, out *[]Change) {
	for _, k := range slices.Sorted(maps.Keys(want)) {
		path := strings.TrimPrefix(prefix+"."+k, ".")
		w, h := want[k], have[k]
		wm, wok := w.(map[string]any)
		hm, hok := h.(map[string]any)
		if wok && hok && !wholeFields[path] {
			compare(hm, wm, path, out)
			continue
		}
		if !reflect.DeepEqual(h, w) {
			*out = append(*out, Change{Field: path, From: h, To: w})
		}
	}
	// Whole fields are declared: leaving one out (no condition, no deliveries, no overrides)
	// empties it.
	for _, path := range slices.Sorted(maps.Keys(wholeFields)) {
		parent, key, _ := strings.Cut(path, ".")
		if prefix != parent {
			continue
		}
		if _, ok := want[key]; !ok && have[key] != nil {
			*out = append(*out, Change{Field: path, From: have[key]})
		}
	}
}
