package runner_test

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/params"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/queries"
)

// fakeRun is a connector whose queries do what their SQL says:
//
//	"block"      waits until the context is cancelled
//	"transient"  fails with query.timeout
//	"auth"       fails with connection.auth_failed
//	"leak"       fails with a driver message that echoes the password
//	anything else returns one row (n = 1)
//
// started receives a value when a blocking query begins.
type fakeRun struct{}

var fakeStarted = make(chan string, 16)

const fakePassword = "s3cret-pass-word"

func (fakeRun) Meta() plugin.Metadata { return plugin.Metadata{ID: "fakerun", Name: "fakerun"} }
func (fakeRun) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{{Key: "password", Type: plugin.TypeString, Secret: true, Label: "l"}}}
}

func (fakeRun) Capabilities() any {
	return plugin.ConnectorCapabilities{PlaceholderStyle: "question", Dialect: "sqlite", ReadOnlyTx: true}
}
func (fakeRun) Messages() plugin.Messages { return nil }
func (fakeRun) Open(context.Context, plugin.OpenOptions) (plugin.Conn, error) {
	return fakeConn{}, nil
}

type fakeConn struct{}

func (fakeConn) Ping(context.Context) (plugin.ServerInfo, error) {
	return plugin.ServerInfo{Version: "fake"}, nil
}

func (fakeConn) Schema(context.Context) (*plugin.DBSchema, error) {
	return &plugin.DBSchema{}, nil
}
func (fakeConn) CanWrite(context.Context) (*bool, error) { return nil, nil }
func (fakeConn) Close() error                            { return nil }
func (fakeConn) Query(ctx context.Context, q plugin.BoundQuery, _ plugin.QueryOptions) (plugin.RowStream, error) {
	switch {
	case strings.Contains(q.SQL, "block"):
		fakeStarted <- q.SQL
		<-ctx.Done()
		return nil, plugin.NewConnError(plugin.ErrCodeQueryCancelled, ctx.Err())
	case strings.Contains(q.SQL, "transient"):
		return nil, plugin.NewConnError(plugin.ErrCodeQueryTimeout, errors.New("canceling statement due to statement timeout"))
	case strings.Contains(q.SQL, "auth"):
		return nil, plugin.NewConnError(plugin.ErrCodeAuthFailed, errors.New("password authentication failed"))
	case strings.Contains(q.SQL, "leak"):
		return nil, plugin.NewConnError(plugin.ErrCodeQueryFailed, errors.New("syntax error near "+fakePassword))
	}
	return &rows{cols: []plugin.Column{{Name: "n", Type: plugin.TypeInt}}, data: [][]any{{int64(1)}}}, nil
}

type rows struct {
	cols []plugin.Column
	data [][]any
	i    int
}

func (r *rows) Columns() []plugin.Column { return r.cols }
func (r *rows) Next() bool               { r.i++; return r.i <= len(r.data) }
func (r *rows) Row() []any               { return r.data[r.i-1] }
func (r *rows) Err() error               { return nil }
func (r *rows) Truncated() bool          { return false }
func (r *rows) Close() error             { return nil }

func init() {
	plugin.Register(plugin.KindConnector, "fakerun", func() plugin.Plugin { return fakeRun{} })
}

func queriesInput(slug string, conn uuid.UUID, sql string) queries.Input {
	return queries.Input{Title: slug, Slug: slug, ConnectionID: conn, SQL: sql}
}

func queriesInputParams(slug string, conn uuid.UUID, sql string, defs ...params.Definition) queries.Input {
	in := queriesInput(slug, conn, sql)
	in.Params = defs
	return in
}
