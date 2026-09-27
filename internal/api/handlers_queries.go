package api

import (
	"context"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/params"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/store"
)

type queryHandlers struct {
	svc *queries.Service
}

func toParams(ps []store.QueryParam) []gen.QueryParam {
	out := make([]gen.QueryParam, len(ps))
	for i, p := range ps {
		out[i] = gen.QueryParam{Name: p.Name, Type: gen.ParamType(p.Type), Default: p.Default}
	}
	return out
}

func fromParams(ps *[]gen.QueryParam) []params.Definition {
	if ps == nil {
		return nil
	}
	out := make([]params.Definition, len(*ps))
	for i, p := range *ps {
		out[i] = params.Definition{Name: p.Name, Type: params.Type(p.Type), Default: p.Default}
	}
	return out
}

func toVersionInfo(v *store.QueryVersion, author string) gen.QueryVersionInfo {
	return gen.QueryVersionInfo{Number: v.Number, Note: v.Note, RestoredFrom: v.RestoredFrom, CreatedAt: v.CreatedAt, AuthorName: author}
}

func toSummary(v *queries.View) gen.QuerySummary {
	s := gen.QuerySummary{
		Id: v.Query.ID, ManagedBy: gen.ManagedBy(v.Query.ManagedBy), Title: v.Query.Title, Slug: v.Query.Slug, Description: v.Query.Description,
		ConnectionId: v.Query.ConnectionID, ConnectionName: v.ConnectionName, Driver: v.Driver,
		AuthorName: v.AuthorName, ReportCount: v.ReportCount, UpdatedAt: v.Query.UpdatedAt,
	}
	if v.Current != nil {
		s.CurrentVersion = v.Current.Number
		if v.Current.CreatedAt.After(s.UpdatedAt) {
			s.UpdatedAt = v.Current.CreatedAt
		}
	}
	return s
}

func toQuery(v *queries.View) gen.Query {
	s := toSummary(v)
	q := gen.Query{
		Id: s.Id, ManagedBy: s.ManagedBy, Title: s.Title, Slug: s.Slug, Description: s.Description, ConnectionId: s.ConnectionId,
		ConnectionName: s.ConnectionName, Driver: s.Driver, AuthorName: s.AuthorName, ReportCount: s.ReportCount,
		UpdatedAt: s.UpdatedAt, CurrentVersion: s.CurrentVersion, CreatedAt: v.Query.CreatedAt, Version: v.Query.Version,
		Params: []gen.QueryParam{},
	}
	if v.Current != nil {
		q.Sql = v.Current.SQL
		q.Params = toParams(v.Current.Params)
		q.Current = toVersionInfo(v.Current, v.AuthorName)
	}
	return q
}

func (h *queryHandlers) ListQueries(ctx context.Context, _ gen.ListQueriesRequestObject) (gen.ListQueriesResponseObject, error) {
	list, err := h.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.ListQueries200JSONResponse{Items: make([]gen.QuerySummary, len(list))}
	for i := range list {
		out.Items[i] = toSummary(&list[i])
	}
	return out, nil
}

func (h *queryHandlers) GetQuery(ctx context.Context, req gen.GetQueryRequestObject) (gen.GetQueryResponseObject, error) {
	v, err := h.svc.Get(ctx, req.QueryId)
	if err != nil {
		return nil, err
	}
	return gen.GetQuery200JSONResponse(toQuery(v)), nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (h *queryHandlers) CreateQuery(ctx context.Context, req gen.CreateQueryRequestObject) (gen.CreateQueryResponseObject, error) {
	b := req.Body
	v, err := h.svc.Create(ctx, PrincipalFrom(ctx), queries.Input{
		Title: b.Title, Slug: deref(b.Slug), Description: deref(b.Description), ConnectionID: b.ConnectionId,
		SQL: b.Sql, Params: fromParams(b.Params), Note: deref(b.Note),
	})
	if err != nil {
		return nil, err
	}
	return gen.CreateQuery201JSONResponse(toQuery(v)), nil
}

func (h *queryHandlers) UpdateQuery(ctx context.Context, req gen.UpdateQueryRequestObject) (gen.UpdateQueryResponseObject, error) {
	b := req.Body
	patch := queries.Patch{Version: b.Version, Title: b.Title, Slug: b.Slug, Description: b.Description, ConnectionID: b.ConnectionId, SQL: b.Sql, Note: deref(b.Note)}
	if b.Params != nil {
		ps := fromParams(b.Params)
		patch.Params = &ps
	}
	v, err := h.svc.Update(ctx, PrincipalFrom(ctx), req.QueryId, patch)
	if err != nil {
		return nil, err
	}
	return gen.UpdateQuery200JSONResponse(toQuery(v)), nil
}

func (h *queryHandlers) DeleteQuery(ctx context.Context, req gen.DeleteQueryRequestObject) (gen.DeleteQueryResponseObject, error) {
	if err := h.svc.Delete(ctx, req.QueryId); err != nil {
		return nil, err
	}
	return gen.DeleteQuery204Response{}, nil
}

func (h *queryHandlers) ListQueryVersions(ctx context.Context, req gen.ListQueryVersionsRequestObject) (gen.ListQueryVersionsResponseObject, error) {
	vs, err := h.svc.Versions(ctx, req.QueryId)
	if err != nil {
		return nil, err
	}
	out := gen.ListQueryVersions200JSONResponse{Items: make([]gen.QueryVersionInfo, len(vs))}
	for i := range vs {
		out.Items[i] = toVersionInfo(&vs[i].QueryVersion, vs[i].AuthorName)
	}
	return out, nil
}

func (h *queryHandlers) GetQueryVersion(ctx context.Context, req gen.GetQueryVersionRequestObject) (gen.GetQueryVersionResponseObject, error) {
	v, err := h.svc.Version(ctx, req.QueryId, req.Number)
	if err != nil {
		return nil, err
	}
	info := toVersionInfo(&v.QueryVersion, v.AuthorName)
	return gen.GetQueryVersion200JSONResponse{
		Number: info.Number, Note: info.Note, RestoredFrom: info.RestoredFrom, CreatedAt: info.CreatedAt,
		AuthorName: info.AuthorName, Sql: v.SQL, Params: toParams(v.Params),
	}, nil
}

func (h *queryHandlers) RestoreQueryVersion(ctx context.Context, req gen.RestoreQueryVersionRequestObject) (gen.RestoreQueryVersionResponseObject, error) {
	v, err := h.svc.Restore(ctx, PrincipalFrom(ctx), req.QueryId, req.Body.Number, req.Body.Version)
	if err != nil {
		return nil, err
	}
	return gen.RestoreQueryVersion200JSONResponse(toQuery(v)), nil
}

func (h *queryHandlers) PreviewQuery(ctx context.Context, req gen.PreviewQueryRequestObject) (gen.PreviewQueryResponseObject, error) {
	b := req.Body
	in := queries.PreviewInput{ConnectionID: b.ConnectionId, SQL: b.Sql, Params: fromParams(b.Params), Timezone: deref(b.Timezone)}
	if b.Values != nil {
		in.Values = *b.Values
	}
	if b.Limit != nil {
		in.Limit = *b.Limit
	}
	res, err := h.svc.Preview(ctx, in)
	if err != nil {
		return nil, err
	}
	out := gen.PreviewQuery200JSONResponse{
		Columns: make([]gen.ResultColumn, len(res.Columns)), Rows: make([][]any, len(res.Rows)),
		Truncated: res.Truncated, DurationMs: res.DurationMS, Params: make([]gen.ResolvedParam, len(res.Params)), Timezone: res.Timezone,
	}
	for i, c := range res.Columns {
		tz := c.WithTimeZone
		out.Columns[i] = gen.ResultColumn{Name: c.Name, Type: gen.ResultColumnType(c.Type), DbType: c.DBType, WithTimeZone: &tz}
	}
	for i, row := range res.Rows {
		out.Rows[i] = make([]any, len(row))
		for j, v := range row {
			out.Rows[i][j] = plugin.JSONValue(v)
		}
	}
	for i, p := range res.Params {
		out.Params[i] = gen.ResolvedParam{Name: p.Name, Type: gen.ParamType(p.Type), Value: p.Display, Builtin: p.Builtin}
	}
	return out, nil
}
