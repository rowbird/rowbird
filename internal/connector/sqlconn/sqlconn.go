// Package sqlconn implements plugin.Conn query execution on top of database/sql, for the connectors
// whose drivers speak it. Each connector supplies a Driver with its dialect-specific parts.
package sqlconn

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/rowbird/rowbird/internal/logging"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/sqlscan"
)

// Driver holds what differs between databases.
type Driver interface {
	Dialect() sqlscan.Dialect
	// TxOptions returns the transaction to run a query in, or nil to run without one.
	TxOptions(opts plugin.QueryOptions) *sql.TxOptions
	// SessionStatements run inside the transaction (or on the pinned connection) before the query,
	// for example "SET LOCAL statement_timeout = 5000".
	SessionStatements(opts plugin.QueryOptions) []string
	// Column maps a result column. An empty Type marks a dynamic column whose type is inferred
	// from the first row (SQLite expressions).
	Column(ct *sql.ColumnType) plugin.Column
	// Convert turns a scanned value into the normalized Go type for col. nil stays nil.
	Convert(col plugin.Column, v any) (any, error)
	// QueryError maps a driver error from a query to a ConnError, or returns nil to use the default.
	QueryError(err error) *plugin.ConnError
}

// ArgsAdjuster is an optional Driver extension that rewrites the query arguments, for example to
// select a protocol mode.
type ArgsAdjuster interface {
	AdjustArgs(opts plugin.QueryOptions, args []any) []any
}

// ServerCanceller is an optional Driver extension for databases whose driver only closes the
// socket on cancel (MySQL): it arranges for the running statement to be stopped on the server
// when ctx ends early. The returned stop function disarms it once the query is done.
type ServerCanceller interface {
	ServerCancel(ctx context.Context, db *sql.DB, conn *sql.Conn) (stop func(), err error)
}

// Conn runs queries on db. Closers (for example an SSH tunnel) are closed after db.
type Conn struct {
	DB *sql.DB
	// MultiDB, when set, returns the pool used for queries that allow several statements (MySQL
	// enables them per connection pool).
	MultiDB func() (*sql.DB, error)
	Driver  Driver
	Closers []io.Closer
	Logger  *slog.Logger
}

// clientGrace lets the server-side timeout fire first, which gives a clearer error, before the
// context deadline cancels the query from the client.
const clientGrace = 2 * time.Second

// Close closes the pool and the extra closers.
func (c *Conn) Close() error {
	err := c.DB.Close()
	for _, cl := range c.Closers {
		err = errors.Join(err, cl.Close())
	}
	return err
}

// Query runs one query under opts and returns a stream of normalized rows.
func (c *Conn) Query(ctx context.Context, q plugin.BoundQuery, opts plugin.QueryOptions) (plugin.RowStream, error) {
	if !opts.AllowMultiStatement && sqlscan.CountStatements(c.Driver.Dialect(), q.SQL) > 1 {
		return nil, plugin.NewConnError(plugin.ErrCodeMultiStatement, nil)
	}
	qctx, cancel := context.WithCancel(ctx)
	if opts.Timeout > 0 {
		cancel()
		qctx, cancel = context.WithTimeout(ctx, opts.Timeout+clientGrace)
	}
	fail := func(err error) (plugin.RowStream, error) {
		mapped := c.mapError(qctx, ctx, err)
		cancel()
		return nil, mapped
	}

	db := c.DB
	if opts.AllowMultiStatement && c.MultiDB != nil {
		var err error
		if db, err = c.MultiDB(); err != nil {
			return fail(err)
		}
	}
	conn, err := db.Conn(qctx)
	if err != nil {
		return fail(err)
	}
	type querier interface {
		ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
		QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	}
	var (
		target querier = conn
		tx     *sql.Tx
	)
	stopCancel := func() {}
	cleanup := func() {
		stopCancel()
		if tx != nil {
			_ = tx.Rollback() // queries never commit
		}
		_ = conn.Close()
		cancel()
	}
	if sc, ok := c.Driver.(ServerCanceller); ok {
		stop, err := sc.ServerCancel(qctx, db, conn)
		if err != nil {
			cleanup()
			return nil, c.mapError(qctx, ctx, err)
		}
		stopCancel = stop
	}
	if txo := c.Driver.TxOptions(opts); txo != nil {
		if tx, err = conn.BeginTx(qctx, txo); err != nil {
			cleanup()
			return nil, c.mapError(qctx, ctx, err)
		}
		target = tx
	}
	for _, stmt := range c.Driver.SessionStatements(opts) {
		if _, err := target.ExecContext(qctx, stmt); err != nil {
			cleanup()
			return nil, c.mapError(qctx, ctx, err)
		}
	}
	args := q.Args
	if a, ok := c.Driver.(ArgsAdjuster); ok {
		args = a.AdjustArgs(opts, args)
	}
	rows, err := target.QueryContext(qctx, q.SQL, args...)
	if err != nil {
		cleanup()
		return nil, c.mapError(qctx, ctx, err)
	}
	s := &stream{rows: rows, conn: c, max: opts.MaxRows, cleanup: cleanup, qctx: qctx, parent: ctx}
	if err := s.init(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// mapError classifies err. Context errors win: a deadline is a timeout and a cancelled parent is a
// user cancel, whatever the driver says.
func (c *Conn) mapError(qctx, parent context.Context, err error) error {
	switch {
	case errors.Is(parent.Err(), context.Canceled):
		return plugin.NewConnError(plugin.ErrCodeQueryCancelled, err)
	case errors.Is(qctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		return plugin.NewConnError(plugin.ErrCodeQueryTimeout, err)
	}
	if ce, ok := plugin.AsConnError(err); ok {
		return ce
	}
	if ce := c.Driver.QueryError(err); ce != nil {
		return ce
	}
	if c.Logger != nil {
		c.Logger.DebugContext(parent, "query failed", "error", logging.RedactString(err.Error()))
	}
	return plugin.NewConnError(plugin.ErrCodeQueryFailed, err)
}

type stream struct {
	rows      *sql.Rows
	conn      *Conn
	cols      []plugin.Column
	max       int
	count     int
	truncated bool
	current   []any
	pending   []any // first row, read ahead to infer dynamic column types
	err       error
	cleanup   func()
	closed    bool
	qctx      context.Context
	parent    context.Context
}

func (s *stream) init() error {
	cts, err := s.rows.ColumnTypes()
	if err != nil {
		return s.conn.mapError(s.qctx, s.parent, err)
	}
	s.cols = make([]plugin.Column, len(cts))
	dynamic := false
	for i, ct := range cts {
		s.cols[i] = s.conn.Driver.Column(ct)
		if s.cols[i].Name == "" {
			s.cols[i].Name = ct.Name()
		}
		dynamic = dynamic || s.cols[i].Type == ""
	}
	if !dynamic {
		return nil
	}
	raw, ok, err := s.scan()
	if err != nil {
		return err
	}
	for i := range s.cols {
		if s.cols[i].Type == "" {
			s.cols[i].Type = plugin.TypeUnknown
			if ok {
				s.cols[i].Type = inferType(raw[i])
			}
		}
	}
	if ok {
		s.pending = raw
	}
	return nil
}

func inferType(v any) plugin.ValueType {
	switch v.(type) {
	case int64:
		return plugin.TypeInt
	case float64:
		return plugin.TypeFloat
	case bool:
		return plugin.TypeBool
	case time.Time:
		return plugin.TypeDateTime
	case []byte:
		return plugin.TypeBinary
	case string:
		return plugin.TypeText
	}
	return plugin.TypeUnknown
}

func (s *stream) scan() ([]any, bool, error) {
	if !s.rows.Next() {
		if err := s.rows.Err(); err != nil {
			return nil, false, s.conn.mapError(s.qctx, s.parent, err)
		}
		return nil, false, nil
	}
	raw := make([]any, len(s.cols))
	ptrs := make([]any, len(raw))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := s.rows.Scan(ptrs...); err != nil {
		return nil, false, s.conn.mapError(s.qctx, s.parent, err)
	}
	return raw, true, nil
}

func (s *stream) Columns() []plugin.Column { return s.cols }

func (s *stream) Next() bool {
	if s.err != nil || s.closed {
		return false
	}
	var raw []any
	var ok bool
	if s.pending != nil {
		raw, ok, s.pending = s.pending, true, nil
	} else {
		raw, ok, s.err = s.scan()
	}
	if s.err != nil || !ok {
		return false
	}
	if s.max > 0 && s.count >= s.max {
		s.truncated = true
		return false
	}
	row := make([]any, len(raw))
	for i, v := range raw {
		if v == nil {
			continue
		}
		cv, err := s.conn.Driver.Convert(s.cols[i], v)
		if err != nil {
			s.err = plugin.NewConnError(plugin.ErrCodeQueryFailed, err)
			return false
		}
		row[i] = cv
	}
	s.current = row
	s.count++
	return true
}

func (s *stream) Row() []any      { return s.current }
func (s *stream) Err() error      { return s.err }
func (s *stream) Truncated() bool { return s.truncated }

func (s *stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	err := s.rows.Close()
	s.cleanup()
	return err
}
