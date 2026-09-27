package gitops

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Pos is a position in a file; lines and columns start at 1.
type Pos struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

func (p Pos) String() string {
	switch {
	case p.File == "":
		return fmt.Sprintf("line %d", p.Line)
	case p.Line == 0:
		return p.File
	case p.Column == 0:
		return fmt.Sprintf("%s:%d", p.File, p.Line)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Column)
}

// Error is a problem in a document, where it happened.
type Error struct {
	Pos     Pos    `json:"pos"`
	Message string `json:"message"`
}

func (e Error) Error() string { return e.Pos.String() + ": " + e.Message }

// Errors are all the problems found; parsing and planning report every one, not just the first.
type Errors []Error

func (es Errors) Error() string {
	lines := make([]string, len(es))
	for i, e := range es {
		lines[i] = e.Error()
	}
	return strings.Join(lines, "\n")
}

// Err returns es as an error, or nil when empty.
func (es Errors) Err() error {
	if len(es) == 0 {
		return nil
	}
	return es
}

// AsErrors extracts the positioned errors of err.
func AsErrors(err error) (Errors, bool) {
	var es Errors
	if errors.As(err, &es) {
		return es, true
	}
	return nil, false
}

var (
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	envRe  = regexp.MustCompile(`^\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}$`)
	lineRe = regexp.MustCompile(`^line (\d+): (.*)$`)
)

// EnvName returns NAME when v is the placeholder ${env:NAME}.
func EnvName(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	m := envRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Placeholder writes ${env:NAME}.
func Placeholder(name string) string { return "${env:" + name + "}" }

// Parse reads the documents of one file. Empty documents are skipped.
func Parse(file string, data []byte) ([]*Document, error) {
	var docs []*Document
	var errs Errors
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			errs = append(errs, yamlError(file, err)...)
			break
		}
		if len(n.Content) == 0 || (n.Content[0].Kind == yaml.ScalarNode && n.Content[0].Tag == "!!null") {
			continue
		}
		d, derrs := document(file, n.Content[0])
		errs = append(errs, derrs...)
		if d != nil {
			docs = append(docs, d)
		}
	}
	return docs, errs.Err()
}

// ParseFiles reads a file, or every .yaml and .yml file under a directory in name order.
func ParseFiles(path string) ([]*Document, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var files []string
	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && (strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")) {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		slices.Sort(files)
	} else {
		files = []string{path}
	}
	var docs []*Document
	var errs Errors
	for _, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // the operator names the files to apply
		if err != nil {
			return nil, err
		}
		ds, err := Parse(f, data)
		if es, ok := AsErrors(err); ok {
			errs = append(errs, es...)
		} else if err != nil {
			return nil, err
		}
		docs = append(docs, ds...)
	}
	errs = append(errs, checkSet(docs)...)
	if len(errs) > 0 {
		return nil, errs
	}
	return docs, nil
}

// ParseSet reads documents given as text (an upload or a paste) and checks them as a set.
func ParseSet(file string, data []byte) ([]*Document, error) {
	docs, err := Parse(file, data)
	if err != nil {
		return nil, err
	}
	if errs := checkSet(docs); len(errs) > 0 {
		return nil, errs
	}
	return docs, nil
}

func yamlError(file string, err error) Errors {
	var te *yaml.TypeError
	var msgs []string
	if errors.As(err, &te) {
		msgs = te.Errors
	} else {
		msgs = []string{strings.TrimPrefix(err.Error(), "yaml: ")}
	}
	out := make(Errors, 0, len(msgs))
	for _, m := range msgs {
		pos := Pos{File: file}
		if sm := lineRe.FindStringSubmatch(m); sm != nil {
			pos.Line, _ = strconv.Atoi(sm[1])
			m = sm[2]
		}
		out = append(out, Error{Pos: pos, Message: m})
	}
	return out
}

func at(file string, n *yaml.Node) Pos { return Pos{File: file, Line: n.Line, Column: n.Column} }

// document reads one document node: the envelope, then the spec of its kind.
func document(file string, n *yaml.Node) (*Document, Errors) {
	pos := at(file, n)
	if n.Kind != yaml.MappingNode {
		return nil, Errors{{Pos: pos, Message: "a document must be a mapping with apiVersion, kind, metadata and spec"}}
	}
	d := &Document{Pos: pos}
	var errs Errors
	var spec *yaml.Node
	fields := map[string]*yaml.Node{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		switch k.Value {
		case "apiVersion", "kind", "metadata":
			fields[k.Value] = v
		case "spec":
			spec = v
		default:
			errs = append(errs, Error{Pos: at(file, k), Message: fmt.Sprintf("unknown field %q", k.Value)})
		}
	}
	if v := fields["apiVersion"]; v == nil || v.Value != APIVersion {
		return nil, append(errs, Error{Pos: pos, Message: fmt.Sprintf("apiVersion must be %s", APIVersion)})
	}
	d.APIVersion = APIVersion
	if v := fields["kind"]; v != nil {
		d.Kind = v.Value
	}
	if !slices.Contains(Kinds, d.Kind) {
		return nil, append(errs, Error{Pos: pos, Message: fmt.Sprintf("kind must be one of %s", strings.Join(Kinds, ", "))})
	}
	if v := fields["metadata"]; v != nil {
		errs = append(errs, decode(file, v, &d.Metadata)...)
	}
	if !nameRe.MatchString(d.Metadata.Name) {
		errs = append(errs, Error{Pos: pos, Message: "metadata.name must be lowercase letters, digits, - and _ (at most 63)"})
	}
	if spec == nil {
		return nil, append(errs, Error{Pos: pos, Message: "spec is missing"})
	}
	switch d.Kind {
	case KindConnection:
		d.Connection = &ConnectionSpec{}
		errs = append(errs, decode(file, spec, d.Connection)...)
	case KindChannel:
		d.Channel = &ChannelSpec{}
		errs = append(errs, decode(file, spec, d.Channel)...)
	case KindQuery:
		d.Query = &QuerySpec{}
		errs = append(errs, decode(file, spec, d.Query)...)
	case KindReport:
		d.Report = &ReportSpec{}
		errs = append(errs, decode(file, spec, d.Report)...)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	if errs := required(d); len(errs) > 0 {
		return nil, errs
	}
	return d, nil
}

// decode fills v from n, reporting unknown fields and type errors at their positions.
func decode(file string, n *yaml.Node, v any) Errors {
	errs := known(file, n, reflect.TypeOf(v))
	if err := n.Decode(v); err != nil {
		errs = append(errs, yamlError(file, err)...)
	}
	return errs
}

// known reports keys of n that t has no field for. Free-form maps are not checked: plugin schemas
// validate them.
func known(file string, n *yaml.Node, t reflect.Type) Errors {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var errs Errors
	switch {
	case t.Kind() == reflect.Struct && n.Kind == yaml.MappingNode:
		fields := map[string]reflect.StructField{}
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name != "" && name != "-" {
				fields[name] = f
			}
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			f, ok := fields[k.Value]
			if !ok {
				errs = append(errs, Error{Pos: at(file, k), Message: fmt.Sprintf("unknown field %q", k.Value)})
				continue
			}
			errs = append(errs, known(file, v, f.Type)...)
		}
	case t.Kind() == reflect.Slice && n.Kind == yaml.SequenceNode:
		for _, item := range n.Content {
			errs = append(errs, known(file, item, t.Elem())...)
		}
	}
	return errs
}

// required checks what every document of a kind needs.
func required(d *Document) Errors {
	var errs Errors
	need := func(ok bool, msg string) {
		if !ok {
			errs = append(errs, Error{Pos: d.Pos, Message: msg})
		}
	}
	switch d.Kind {
	case KindConnection:
		need(d.Connection.Driver != "", "spec.driver is required")
	case KindChannel:
		need(d.Channel.Type != "", "spec.type is required")
	case KindQuery:
		need(d.Query.Connection != "", "spec.connection is required")
		need(strings.TrimSpace(d.Query.SQL) != "", "spec.sql is required")
	case KindReport:
		r := d.Report
		q := r.Query
		switch {
		case q.Ref != "" && (q.Connection != "" || q.SQL != "" || len(q.Params) > 0):
			need(false, "spec.query takes either ref or an inline query (connection, sql, params), not both")
		case q.Ref == "":
			need(q.Connection != "" && strings.TrimSpace(q.SQL) != "", "spec.query needs ref, or connection and sql")
		}
		need(r.Schedule.Cron != "", "spec.schedule.cron is required")
		for i, dl := range r.Deliveries {
			need(dl.Channel != "" && dl.Mode != "", fmt.Sprintf("spec.deliveries[%d] needs channel and mode", i))
			if dl.Link != nil && dl.Link.ExpiresIn != "" {
				_, err := ParseDuration(dl.Link.ExpiresIn)
				need(err == nil, fmt.Sprintf("spec.deliveries[%d].link.expiresIn: %v", i, err))
			}
		}
		if r.Condition != nil {
			for i, rule := range r.Condition.Rules {
				_, ok := rule["type"].(string)
				need(ok, fmt.Sprintf("spec.condition.rules[%d] needs a type", i))
			}
		}
	}
	return errs
}

// checkSet reports documents of the same kind and name.
func checkSet(docs []*Document) Errors {
	var errs Errors
	seen := map[string]*Document{}
	for _, d := range docs {
		if first, ok := seen[d.Key()]; ok {
			errs = append(errs, Error{Pos: d.Pos, Message: fmt.Sprintf("%s %q is already defined at %s", d.Kind, d.Metadata.Name, first.Pos)})
			continue
		}
		seen[d.Key()] = d
	}
	return errs
}

var durationUnits = []struct {
	suffix  string
	seconds int
}{{"d", 86400}, {"h", 3600}, {"m", 60}, {"s", 1}}

// ParseDuration reads 30d, 12h, 90m or 3600s into seconds.
func ParseDuration(s string) (int, error) {
	s = strings.TrimSpace(s)
	for _, u := range durationUnits {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			v, err := strconv.Atoi(n)
			if err != nil || v <= 0 {
				break
			}
			return v * u.seconds, nil
		}
	}
	return 0, fmt.Errorf("%q is not a duration such as 30d, 12h, 90m or 3600s", s)
}

// FormatDuration writes seconds with the largest unit that divides them.
func FormatDuration(seconds int) string {
	for _, u := range durationUnits {
		if seconds%u.seconds == 0 {
			return strconv.Itoa(seconds/u.seconds) + u.suffix
		}
	}
	return strconv.Itoa(seconds) + "s"
}
