// Package queries manages named, versioned SQL and its preview (docs/spec/03-flows.md, section 3).
package queries

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/connections"
	"github.com/rowbird/rowbird/internal/params"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/slug"
	"github.com/rowbird/rowbird/internal/sqlscan"
	"github.com/rowbird/rowbird/internal/store"
)

// Limits (docs/spec/03-flows.md: preview defaults to 100 rows and 30 seconds).
const (
	maxSQLBytes        = 1 << 20
	DefaultPreviewRows = 100
	MaxPreviewRows     = 1000
	PreviewTimeout     = 30 * time.Second
)

// Errors.
var (
	ErrSlugTaken = apperr.New(apperr.KindConflict, "query.slug_taken")
	ErrInUse     = apperr.New(apperr.KindConflict, "query.in_use")
)

// Service manages queries.
type Service struct {
	store  *store.Store
	conns  *connections.Service
	logger *slog.Logger
	now    func() time.Time
}

// Options configure the service.
type Options struct {
	Logger *slog.Logger
	Now    func() time.Time
}

// NewService builds the service.
func NewService(st *store.Store, conns *connections.Service, opts Options) *Service {
	s := &Service{store: st, conns: conns, logger: opts.Logger, now: opts.Now}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// View is a query with its current version and the context the UI shows.
type View struct {
	Query          store.Query
	Current        *store.QueryVersion
	ConnectionName string
	Driver         string
	// ReportCount is how many reports use the query.
	ReportCount int
	// AuthorName is who created the current version.
	AuthorName string
}

// VersionView is a version with its author's name.
type VersionView struct {
	store.QueryVersion
	AuthorName string
}

// authors maps user ids to names; unknown users map to "".
func (s *Service) authors(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	users, err := s.store.Users().GetMany(ctx, ids)
	if err != nil {
		return out
	}
	for _, u := range users {
		out[u.ID] = u.Name
	}
	return out
}

// Input creates a query. Slug is derived from Title when empty.
type Input struct {
	Title        string
	Slug         string
	Description  string
	ConnectionID uuid.UUID
	SQL          string
	Params       []params.Definition
	Note         string
}

// Patch changes a query. SQL and Params, when set, create a new version if they differ from the
// current one.
type Patch struct {
	Version      int64
	Title        *string
	Slug         *string
	Description  *string
	ConnectionID *uuid.UUID
	SQL          *string
	Params       *[]params.Definition
	Note         string
}

// List returns every query with its current version.
func (s *Service) List(ctx context.Context) ([]View, error) {
	qs, err := s.store.Queries().List(ctx)
	if err != nil {
		return nil, err
	}
	conns, err := s.store.Connections().List(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID]store.Connection{}
	for _, c := range conns {
		byID[c.ID] = c
	}
	counts, err := s.store.Reports().CountByQuery(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(qs))
	var authorIDs []uuid.UUID
	for _, q := range qs {
		v := View{Query: q, ConnectionName: byID[q.ConnectionID].Name, Driver: byID[q.ConnectionID].Driver, ReportCount: counts[q.ID]}
		if q.CurrentVersionID != nil {
			if v.Current, err = s.store.Queries().VersionByID(ctx, *q.CurrentVersionID); err != nil {
				return nil, err
			}
			if v.Current.CreatedBy != nil {
				authorIDs = append(authorIDs, *v.Current.CreatedBy)
			}
		}
		out = append(out, v)
	}
	names := s.authors(ctx, authorIDs)
	for i := range out {
		if out[i].Current != nil && out[i].Current.CreatedBy != nil {
			out[i].AuthorName = names[*out[i].Current.CreatedBy]
		}
	}
	return out, nil
}

// Get returns one query with its current version.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*View, error) {
	q, err := s.store.Queries().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	v := &View{Query: *q}
	reports, err := s.store.Reports().ListByQuery(ctx, id)
	if err != nil {
		return nil, err
	}
	v.ReportCount = len(reports)
	if c, err := s.store.Connections().Get(ctx, q.ConnectionID); err == nil {
		v.ConnectionName, v.Driver = c.Name, c.Driver
	}
	if q.CurrentVersionID != nil {
		if v.Current, err = s.store.Queries().VersionByID(ctx, *q.CurrentVersionID); err != nil {
			return nil, err
		}
		if v.Current.CreatedBy != nil {
			v.AuthorName = s.authors(ctx, []uuid.UUID{*v.Current.CreatedBy})[*v.Current.CreatedBy]
		}
	}
	return v, nil
}

// Create stores a query with its first version.
func (s *Service) Create(ctx context.Context, p *auth.Principal, in Input) (*View, error) {
	if in.Slug == "" {
		in.Slug = Slugify(in.Title)
	}
	var fe []apperr.FieldError
	fe = append(fe, checkMeta(in.Title, in.Slug, in.Description)...)
	dialect, err := s.connectionDialect(ctx, in.ConnectionID)
	if err != nil {
		fe = append(fe, apperr.Field("connection_id", "validation.invalid_value"))
	}
	fe = append(fe, checkSQL(dialect, in.SQL, in.Params)...)
	if len(fe) > 0 {
		return nil, apperr.Invalid(fe...)
	}
	q := &store.Query{Title: strings.TrimSpace(in.Title), Slug: in.Slug, Description: strings.TrimSpace(in.Description), ConnectionID: in.ConnectionID}
	q.CreatedBy = &p.UserID
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Queries().Create(ctx, q); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return ErrSlugTaken
			}
			return err
		}
		v := &store.QueryVersion{QueryID: q.ID, SQL: in.SQL, Params: toStore(in.Params), Note: strings.TrimSpace(in.Note)}
		v.CreatedBy = &p.UserID
		if err := s.store.Queries().AddVersion(ctx, v); err != nil {
			return err
		}
		q.CurrentVersionID = &v.ID
		return s.store.Queries().Update(ctx, q, "current_version_id")
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, q.ID)
}

// Update applies a patch. A change of SQL or parameters creates a new version.
func (s *Service) Update(ctx context.Context, p *auth.Principal, id uuid.UUID, patch Patch) (*View, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := store.CheckManaged(ctx, cur.Query.ManagedBy); err != nil {
		return nil, err
	}
	q := cur.Query
	q.Version = patch.Version
	cols := []string{}
	title, slug, desc := q.Title, q.Slug, q.Description
	if patch.Title != nil {
		title = *patch.Title
		cols = append(cols, "title")
	}
	if patch.Slug != nil {
		slug = *patch.Slug
		cols = append(cols, "slug")
	}
	if patch.Description != nil {
		desc = *patch.Description
		cols = append(cols, "description")
	}
	fe := checkMeta(title, slug, desc)
	if patch.ConnectionID != nil {
		q.ConnectionID = *patch.ConnectionID
		cols = append(cols, "connection_id")
	}
	dialect, err := s.connectionDialect(ctx, q.ConnectionID)
	if err != nil {
		fe = append(fe, apperr.Field("connection_id", "validation.invalid_value"))
	}
	newSQL, newParams := cur.Current.SQL, fromStore(cur.Current.Params)
	if patch.SQL != nil {
		newSQL = *patch.SQL
	}
	if patch.Params != nil {
		newParams = *patch.Params
	}
	fe = append(fe, checkSQL(dialect, newSQL, newParams)...)
	if len(fe) > 0 {
		return nil, apperr.Invalid(fe...)
	}
	q.Title, q.Slug, q.Description = strings.TrimSpace(title), slug, strings.TrimSpace(desc)
	changed := newSQL != cur.Current.SQL || !sameParams(newParams, fromStore(cur.Current.Params))

	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		if changed {
			v := &store.QueryVersion{QueryID: q.ID, SQL: newSQL, Params: toStore(newParams), Note: strings.TrimSpace(patch.Note)}
			v.CreatedBy = &p.UserID
			if err := s.store.Queries().AddVersion(ctx, v); err != nil {
				return err
			}
			q.CurrentVersionID = &v.ID
			cols = append(cols, "current_version_id")
		}
		if err := s.store.Queries().Update(ctx, &q, cols...); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return ErrSlugTaken
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete removes a query that no report uses.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if q, err := s.store.Queries().Get(ctx, id); err == nil {
		if err := store.CheckManaged(ctx, q.ManagedBy); err != nil {
			return err
		}
	}
	reports, err := s.store.Reports().ListByQuery(ctx, id)
	if err != nil {
		return err
	}
	if len(reports) > 0 {
		e := *ErrInUse
		for _, r := range reports {
			e.Dependents = append(e.Dependents, apperr.Dependent{Type: "report", ID: r.ID.String(), Name: r.Title})
		}
		return &e
	}
	return s.store.Queries().Delete(ctx, id)
}

// Current returns a query with its current version.
func (s *Service) Current(ctx context.Context, id uuid.UUID) (*store.Query, *store.QueryVersion, error) {
	q, err := s.store.Queries().Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if q.CurrentVersionID == nil {
		return nil, nil, store.ErrNotFound
	}
	v, err := s.store.Queries().VersionByID(ctx, *q.CurrentVersionID)
	if err != nil {
		return nil, nil, err
	}
	return q, v, nil
}

// Definitions returns a version's parameter definitions.
func Definitions(v *store.QueryVersion) []params.Definition { return fromStore(v.Params) }

// CheckValues validates parameter values that a report supplies for a query's current version:
// every name must be a user parameter of the query, every value must parse, and parameters used
// without a default need a value. Field errors are reported as "<field>.<name>".
func (s *Service) CheckValues(ctx context.Context, id uuid.UUID, values map[string]string, field string) ([]apperr.FieldError, error) {
	q, v, err := s.Current(ctx, id)
	if err != nil {
		return nil, err
	}
	dialect, err := s.connectionDialect(ctx, q.ConnectionID)
	if err != nil {
		return nil, err
	}
	defs := fromStore(v.Params)
	var fe []apperr.FieldError
	for name := range values {
		if !slices.ContainsFunc(defs, func(d params.Definition) bool { return d.Name == name }) {
			fe = append(fe, apperr.Field(field+"."+name, params.CodeUndefined))
		}
	}
	if len(fe) > 0 {
		return fe, nil
	}
	_, err = params.Resolve(params.Names(dialect, v.SQL), defs, values, s.now(), time.UTC)
	if ae, ok := apperr.As(err); ok {
		for _, f := range ae.Fields {
			fe = append(fe, apperr.Field(field+strings.TrimPrefix(f.Field, "values"), f.Code))
		}
		return fe, nil
	}
	return nil, err
}

// Versions lists a query's versions, newest first.
func (s *Service) Versions(ctx context.Context, id uuid.UUID) ([]VersionView, error) {
	if _, err := s.store.Queries().Get(ctx, id); err != nil {
		return nil, err
	}
	vs, err := s.store.Queries().Versions(ctx, id)
	if err != nil {
		return nil, err
	}
	var authorIDs []uuid.UUID
	for _, v := range vs {
		if v.CreatedBy != nil {
			authorIDs = append(authorIDs, *v.CreatedBy)
		}
	}
	names := s.authors(ctx, authorIDs)
	out := make([]VersionView, len(vs))
	for i, v := range vs {
		out[i] = VersionView{QueryVersion: v}
		if v.CreatedBy != nil {
			out[i].AuthorName = names[*v.CreatedBy]
		}
	}
	return out, nil
}

// Version returns one version.
func (s *Service) Version(ctx context.Context, id uuid.UUID, number int) (*VersionView, error) {
	v, err := s.store.Queries().Version(ctx, id, number)
	if err != nil {
		return nil, err
	}
	out := &VersionView{QueryVersion: *v}
	if v.CreatedBy != nil {
		out.AuthorName = s.authors(ctx, []uuid.UUID{*v.CreatedBy})[*v.CreatedBy]
	}
	return out, nil
}

// Restore makes a copy of an old version the current one; history is never rewritten.
func (s *Service) Restore(ctx context.Context, p *auth.Principal, id uuid.UUID, number int, version int64) (*View, error) {
	old, err := s.store.Queries().Version(ctx, id, number)
	if err != nil {
		return nil, err
	}
	q, err := s.store.Queries().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := store.CheckManaged(ctx, q.ManagedBy); err != nil {
		return nil, err
	}
	q.Version = version
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		v := &store.QueryVersion{QueryID: id, SQL: old.SQL, Params: old.Params, RestoredFrom: &old.Number}
		v.CreatedBy = &p.UserID
		if err := s.store.Queries().AddVersion(ctx, v); err != nil {
			return err
		}
		q.CurrentVersionID = &v.ID
		return s.store.Queries().Update(ctx, q, "current_version_id")
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// PreviewInput runs SQL that is not necessarily saved.
type PreviewInput struct {
	ConnectionID uuid.UUID
	SQL          string
	Params       []params.Definition
	Values       map[string]string
	// Timezone resolves built-in dates; empty means the workspace default.
	Timezone string
	Limit    int
}

// PreviewResult is what a preview returns.
type PreviewResult struct {
	Columns    []plugin.Column
	Rows       [][]any
	Truncated  bool
	DurationMS int64
	Params     []params.Resolved
	Timezone   string
}

// Preview runs SQL read-only with a small row cap and a short timeout.
func (s *Service) Preview(ctx context.Context, in PreviewInput) (*PreviewResult, error) {
	dialect, err := s.connectionDialect(ctx, in.ConnectionID)
	if err != nil {
		return nil, apperr.Invalid(apperr.Field("connection_id", "validation.invalid_value"))
	}
	fe := checkSQL(dialect, in.SQL, in.Params)
	tzName := in.Timezone
	if tzName == "" {
		tzName = s.defaultTimezone(ctx)
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil || tzName == "Local" {
		fe = append(fe, apperr.Field("timezone", "validation.invalid_value"))
	}
	if in.Limit < 0 || in.Limit > MaxPreviewRows {
		fe = append(fe, apperr.Field("limit", "validation.range"))
	}
	if len(fe) > 0 {
		return nil, apperr.Invalid(fe...)
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultPreviewRows
	}
	res := &PreviewResult{Rows: [][]any{}, Timezone: tzName}
	out, err := s.Execute(ctx, ExecInput{
		ConnectionID: in.ConnectionID, SQL: in.SQL, Params: in.Params, Values: in.Values, Location: loc,
		MaxRows: limit, Timeout: PreviewTimeout,
	}, SinkFunc(func(row []any) error {
		res.Rows = append(res.Rows, row)
		return nil
	}))
	if err != nil {
		return nil, queryError(err)
	}
	res.Columns, res.Truncated, res.DurationMS, res.Params = out.Columns, out.Truncated, out.DurationMS, out.Params
	return res, nil
}

// ExecInput runs SQL on a saved connection, read-only.
type ExecInput struct {
	ConnectionID uuid.UUID
	SQL          string
	Params       []params.Definition
	Values       map[string]string
	// Location resolves built-in dates.
	Location *time.Location
	// MaxRows and Timeout lower the connection's own limits; zero keeps the connection's.
	MaxRows int
	Timeout time.Duration
}

// ExecResult describes an executed query; the rows went to the sink.
type ExecResult struct {
	Columns    []plugin.Column
	RowCount   int64
	Truncated  bool
	DurationMS int64
	Params     []params.Resolved
}

// Sink receives the result of Execute: the columns once, then every row.
type Sink interface {
	Columns(cols []plugin.Column) error
	Row(row []any) error
}

// SinkFunc is a Sink that only cares about rows.
type SinkFunc func(row []any) error

// Columns implements Sink.
func (SinkFunc) Columns([]plugin.Column) error { return nil }

// Row implements Sink.
func (f SinkFunc) Row(row []any) error { return f(row) }

// Execute resolves the parameters, binds them as driver parameters and streams the rows to sink,
// always in a read-only transaction and never beyond the connection's row and time limits. It is
// the one path from Rowbird to a user database for both the preview and report runs. Parameter
// problems are validation errors on "values.<name>"; database failures are plugin.ConnError.
func (s *Service) Execute(ctx context.Context, in ExecInput, sink Sink) (*ExecResult, error) {
	dialect, err := s.connectionDialect(ctx, in.ConnectionID)
	if err != nil {
		return nil, err
	}
	loc := in.Location
	if loc == nil {
		loc = time.UTC
	}
	resolved, err := params.Resolve(params.Names(dialect, in.SQL), in.Params, in.Values, s.now(), loc)
	if err != nil {
		return nil, err
	}
	conn, c, err := s.conns.Open(ctx, in.ConnectionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	caps := capabilities(c.Driver)
	bound, err := params.Bind(dialect, caps.PlaceholderStyle, in.SQL, resolved)
	if err != nil {
		return nil, err
	}
	limit := c.MaxRows
	if in.MaxRows > 0 {
		limit = min(limit, in.MaxRows)
	}
	timeout := time.Duration(c.QueryTimeoutSeconds) * time.Second
	if in.Timeout > 0 {
		timeout = min(timeout, in.Timeout)
	}
	start := time.Now()
	rs, err := conn.Query(ctx, bound, plugin.QueryOptions{Timeout: timeout, MaxRows: limit, ReadOnly: true, AllowMultiStatement: c.AllowMultiStatement})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	res := &ExecResult{Columns: rs.Columns(), Params: resolved}
	if err := sink.Columns(res.Columns); err != nil {
		return nil, err
	}
	for rs.Next() {
		if err := sink.Row(rs.Row()); err != nil {
			return nil, err
		}
		res.RowCount++
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	res.Truncated = rs.Truncated()
	res.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}

func (s *Service) defaultTimezone(ctx context.Context) string {
	st, err := s.store.Settings().Get(ctx, auth.SettingDefaultTimezone)
	if err != nil {
		return "UTC"
	}
	var tz string
	if json.Unmarshal([]byte(st.Value), &tz) != nil || tz == "" {
		return "UTC"
	}
	return tz
}

func (s *Service) connectionDialect(ctx context.Context, id uuid.UUID) (sqlscan.Dialect, error) {
	c, err := s.store.Connections().Get(ctx, id)
	if err != nil {
		return "", err
	}
	return sqlscan.Dialect(capabilities(c.Driver).Dialect), nil
}

func capabilities(driver string) plugin.ConnectorCapabilities {
	if p, ok := plugin.Get(plugin.KindConnector, driver); ok {
		if caps, ok := p.Capabilities().(plugin.ConnectorCapabilities); ok {
			return caps
		}
	}
	return plugin.ConnectorCapabilities{}
}

// queryError turns a failed query into a 422 with the connector's code. The database message is
// kept out of the response; it may quote data.
func queryError(err error) error {
	if ce, ok := plugin.AsConnError(err); ok {
		return &apperr.Error{Kind: apperr.KindUnprocessable, Code: ce.Code}
	}
	return err
}

// Slugify derives a query slug from a title.
func Slugify(title string) string { return slug.Make(title, "query") }

func checkMeta(title, s, desc string) []apperr.FieldError { return slug.CheckMeta(title, s, desc) }

func checkSQL(d sqlscan.Dialect, sql string, defs []params.Definition) []apperr.FieldError {
	var fe []apperr.FieldError
	switch {
	case strings.TrimSpace(sql) == "":
		fe = append(fe, apperr.Field("sql", "validation.required"))
	case len(sql) > maxSQLBytes:
		fe = append(fe, apperr.Field("sql", "validation.too_long"))
	}
	fe = append(fe, params.ValidateDefinitions(defs)...)
	if d != "" && len(fe) == 0 {
		fe = append(fe, params.CheckUsage(d, sql, defs)...)
	}
	return fe
}

func toStore(defs []params.Definition) []store.QueryParam {
	out := make([]store.QueryParam, len(defs))
	for i, d := range defs {
		out[i] = store.QueryParam{Name: d.Name, Type: string(d.Type), Default: d.Default}
	}
	return out
}

func fromStore(ps []store.QueryParam) []params.Definition {
	out := make([]params.Definition, len(ps))
	for i, p := range ps {
		out[i] = params.Definition{Name: p.Name, Type: params.Type(p.Type), Default: p.Default}
	}
	return out
}

func sameParams(a, b []params.Definition) bool {
	return slices.EqualFunc(a, b, func(x, y params.Definition) bool {
		return x.Name == y.Name && x.Type == y.Type && ((x.Default == nil && y.Default == nil) || (x.Default != nil && y.Default != nil && *x.Default == *y.Default))
	})
}
