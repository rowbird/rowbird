package api

import (
	"context"
	"encoding/json"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/connections"
	"github.com/rowbird/rowbird/internal/plugin"
)

type connectionHandlers struct {
	svc *connections.Service
}

var pluginKinds = []plugin.Kind{plugin.KindConnector, plugin.KindFormatter, plugin.KindCondition, plugin.KindDestination, plugin.KindAIProvider, plugin.KindStorage}

func (h *connectionHandlers) ListPlugins(context.Context, gen.ListPluginsRequestObject) (gen.ListPluginsResponseObject, error) {
	out := gen.ListPlugins200JSONResponse{Plugins: []gen.PluginInfo{}, Messages: map[string]map[string]string{}}
	var catalogs []plugin.Messages
	for _, kind := range pluginKinds {
		for _, p := range plugin.List(kind) {
			m := p.Meta()
			schema, err := toObject(p.ConfigSchema())
			if err != nil {
				return nil, err
			}
			caps, err := toObject(p.Capabilities())
			if err != nil {
				return nil, err
			}
			info := gen.PluginInfo{
				Kind: gen.PluginInfoKind(kind), Id: m.ID, Name: m.Name, Description: m.Description,
				Icon: m.Icon, Version: m.Version, Schema: schema, Capabilities: caps,
			}
			if d, ok := p.(plugin.Destination); ok {
				ds, err := toObject(d.DeliverySchema())
				if err != nil {
					return nil, err
				}
				info.DeliverySchema = &ds
			}
			out.Plugins = append(out.Plugins, info)
			catalogs = append(catalogs, p.Messages())
		}
	}
	out.Messages = plugin.MergeMessages(catalogs...)
	return out, nil
}

// toObject turns a value into a JSON object for free-form API fields.
func toObject(v any) (map[string]any, error) {
	out := map[string]any{}
	if v == nil {
		return out, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if string(b) == "null" {
		return out, nil
	}
	return out, json.Unmarshal(b, &out)
}

func toConnection(v *connections.View) gen.Connection {
	cfg := map[string]any{}
	for k, val := range v.Config {
		cfg[k] = val
	}
	for k, set := range v.SecretsConfigured {
		cfg[k] = map[string]any{"configured": set}
	}
	excluded := v.AIExcludedTables
	if excluded == nil {
		excluded = []string{}
	}
	return gen.Connection{
		Id: v.ID, Name: v.Name, ManagedBy: gen.ManagedBy(v.ManagedBy), Driver: v.Driver, Config: cfg,
		QueryTimeoutSeconds: v.QueryTimeoutSeconds, MaxRows: v.MaxRows, AllowMultiStatement: v.AllowMultiStatement,
		AiExcludedTables: excluded, HasWritePermission: v.HasWritePermission, Status: gen.ConnectionStatus(v.Status),
		ServerVersion: v.ServerVersion, LastCheckedAt: v.LastCheckedAt, LastError: v.LastError,
		SchemaCachedAt: v.SchemaCachedAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Version: v.Version,
	}
}

func (h *connectionHandlers) ListConnections(ctx context.Context, _ gen.ListConnectionsRequestObject) (gen.ListConnectionsResponseObject, error) {
	list, err := h.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.ListConnections200JSONResponse{Items: make([]gen.Connection, len(list))}
	for i := range list {
		out.Items[i] = toConnection(&list[i])
	}
	return out, nil
}

func (h *connectionHandlers) GetConnection(ctx context.Context, req gen.GetConnectionRequestObject) (gen.GetConnectionResponseObject, error) {
	v, err := h.svc.Get(ctx, req.ConnectionId)
	if err != nil {
		return nil, err
	}
	return gen.GetConnection200JSONResponse(toConnection(v)), nil
}

func (h *connectionHandlers) CreateConnection(ctx context.Context, req gen.CreateConnectionRequestObject) (gen.CreateConnectionResponseObject, error) {
	b := req.Body
	in := connections.Input{Name: b.Name, Driver: b.Driver, Config: b.Config, QueryTimeoutSeconds: b.QueryTimeoutSeconds, MaxRows: b.MaxRows, AllowMultiStatement: b.AllowMultiStatement}
	if b.AiExcludedTables != nil {
		in.AIExcludedTables = *b.AiExcludedTables
	}
	v, err := h.svc.Create(ctx, PrincipalFrom(ctx), in, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.CreateConnection201JSONResponse(toConnection(v)), nil
}

func (h *connectionHandlers) UpdateConnection(ctx context.Context, req gen.UpdateConnectionRequestObject) (gen.UpdateConnectionResponseObject, error) {
	b := req.Body
	patch := connections.Patch{Version: b.Version, Name: b.Name, QueryTimeoutSeconds: b.QueryTimeoutSeconds, MaxRows: b.MaxRows, AllowMultiStatement: b.AllowMultiStatement, AIExcludedTables: b.AiExcludedTables}
	if b.Config != nil {
		patch.Config = *b.Config
	}
	v, err := h.svc.Update(ctx, PrincipalFrom(ctx), req.ConnectionId, patch, requestMetaFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.UpdateConnection200JSONResponse(toConnection(v)), nil
}

func (h *connectionHandlers) DeleteConnection(ctx context.Context, req gen.DeleteConnectionRequestObject) (gen.DeleteConnectionResponseObject, error) {
	if err := h.svc.Delete(ctx, PrincipalFrom(ctx), req.ConnectionId, requestMetaFrom(ctx)); err != nil {
		return nil, err
	}
	return gen.DeleteConnection204Response{}, nil
}

func toTestResult(r *connections.TestResult) gen.ConnectionTestResult {
	out := gen.ConnectionTestResult{Ok: r.OK, CanWrite: r.CanWrite}
	if r.OK {
		out.LatencyMs, out.ServerVersion = &r.LatencyMS, &r.ServerVersion
		if r.Schema != nil {
			n := len(r.Schema.Tables)
			out.Tables = &n
		}
		return out
	}
	out.ErrorCode = &r.ErrorCode
	if len(r.ErrorDetail) > 0 {
		out.ErrorDetail = &r.ErrorDetail
	}
	return out
}

func (h *connectionHandlers) TestConnectionConfig(ctx context.Context, req gen.TestConnectionConfigRequestObject) (gen.TestConnectionConfigResponseObject, error) {
	b := req.Body
	res, err := h.svc.Test(ctx, connections.TestInput{ConnectionID: b.ConnectionId, Driver: b.Driver, Config: b.Config})
	if err != nil {
		return nil, err
	}
	return gen.TestConnectionConfig200JSONResponse(toTestResult(res)), nil
}

func (h *connectionHandlers) TestConnection(ctx context.Context, req gen.TestConnectionRequestObject) (gen.TestConnectionResponseObject, error) {
	res, err := h.svc.TestSaved(ctx, req.ConnectionId)
	if err != nil {
		return nil, err
	}
	return gen.TestConnection200JSONResponse(toTestResult(res)), nil
}

func toSchema(s *plugin.DBSchema) gen.DatabaseSchema {
	out := gen.DatabaseSchema{Tables: make([]gen.DatabaseTable, len(s.Tables))}
	for i, t := range s.Tables {
		gt := gen.DatabaseTable{Name: t.Name, Kind: gen.DatabaseTableKind(t.Kind), Columns: make([]gen.DatabaseColumn, len(t.Columns))}
		if t.Schema != "" {
			gt.Schema = &t.Schema
		}
		if t.Comment != "" {
			gt.Comment = &t.Comment
		}
		for j, c := range t.Columns {
			gc := gen.DatabaseColumn{Name: c.Name, Type: gen.DatabaseColumnType(c.Type), DbType: c.DBType, Nullable: c.Nullable}
			if c.Comment != "" {
				gc.Comment = &c.Comment
			}
			gt.Columns[j] = gc
		}
		out.Tables[i] = gt
	}
	return out
}

func (h *connectionHandlers) GetConnectionSchema(ctx context.Context, req gen.GetConnectionSchemaRequestObject) (gen.GetConnectionSchemaResponseObject, error) {
	s, at, err := h.svc.Schema(ctx, req.ConnectionId)
	if err != nil {
		return nil, err
	}
	out := toSchema(s)
	out.CachedAt = at
	return gen.GetConnectionSchema200JSONResponse(out), nil
}

func (h *connectionHandlers) RefreshConnectionSchema(ctx context.Context, req gen.RefreshConnectionSchemaRequestObject) (gen.RefreshConnectionSchemaResponseObject, error) {
	s, at, err := h.svc.RefreshSchema(ctx, req.ConnectionId)
	if err != nil {
		return nil, err
	}
	out := toSchema(s)
	out.CachedAt = at
	return gen.RefreshConnectionSchema200JSONResponse(out), nil
}
