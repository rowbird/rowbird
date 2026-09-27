// Package connectortest is the conformance suite every connector must pass
// (docs/spec/10-quality-and-community.md): type normalization including exact decimals, timeout,
// cancel, read-only enforcement, schema, row limit, multi-statement rejection and error codes.
package connectortest

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
)

// Harness describes how to exercise one connector against one live database.
type Harness struct {
	Connector plugin.Connector
	// Values connect as a user that can write (the suite uses it to check CanWrite).
	Values map[string]any
	// ReadOnlyValues connect as a user without write permission; nil skips that check.
	ReadOnlyValues map[string]any
	// Setup creates the fixture tables (see Fixture) by any means, typically with the admin user.
	Setup func(t *testing.T)
	// Types lists the columns of rb_types in order with the value of its first row. Its second row
	// must be all NULL except id.
	Types []ExpectedColumn
	// SlowSQL runs for at least 10 seconds (pg_sleep, WAITFOR, a recursive CTE...).
	SlowSQL string
	// ParamSQL selects its single parameter back, in the driver's placeholder style.
	ParamSQL string
	// InsertSQL writes one row into rb_rows; used to prove read-only enforcement.
	InsertSQL string
	// Errors maps an error code to configuration values that must produce it.
	Errors map[string]map[string]any
	// Options passed to Open (SQLite directories and so on).
	Options plugin.OpenOptions
}

// ExpectedColumn is one column of the types fixture.
type ExpectedColumn struct {
	Name         string
	Type         plugin.ValueType
	WithTimeZone bool
	Value        any
}

// Fixture documents what Setup must create:
//
//   - rb_types: an id column (integer, 1 and 2) followed by the Types columns; row 1 holds the
//     expected values, row 2 is NULL in every Types column.
//   - rb_rows: a table with an integer column n and exactly 10 rows.
const Fixture = "rb_types, rb_rows"

// Run executes the suite.
func Run(t *testing.T, h Harness) {
	h.Setup(t)
	open := func(t *testing.T, values map[string]any) plugin.Conn {
		t.Helper()
		opts := h.Options
		opts.Values = values
		if opts.Dial == nil {
			opts.Dial = netx.NewDialer(netx.PolicyOpen).DialContext
		}
		c, err := h.Connector.Open(t.Context(), opts)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	caps, _ := h.Connector.Capabilities().(plugin.ConnectorCapabilities)
	query := func(t *testing.T, c plugin.Conn, sql string, opts plugin.QueryOptions, args ...any) ([]plugin.Column, [][]any, bool, error) {
		t.Helper()
		rs, err := c.Query(t.Context(), plugin.BoundQuery{SQL: sql, Args: args}, opts)
		if err != nil {
			return nil, nil, false, err
		}
		defer func() { _ = rs.Close() }()
		var rows [][]any
		for rs.Next() {
			rows = append(rows, rs.Row())
		}
		return rs.Columns(), rows, rs.Truncated(), rs.Err()
	}
	readOnly := plugin.QueryOptions{ReadOnly: true, Timeout: 30 * time.Second, MaxRows: 1000}

	t.Run("metadata", func(t *testing.T) {
		m := h.Connector.Meta()
		if m.ID == "" || m.Name == "" || h.Connector.ConfigSchema() == nil {
			t.Fatal("metadata or schema missing")
		}
		msgs := h.Connector.Messages()
		for _, locale := range []string{"en", "pt-BR"} {
			if msgs[locale][m.Name] == "" {
				t.Errorf("%s has no %s translation for its name", m.ID, locale)
			}
		}
		for _, f := range h.Connector.ConfigSchema().Fields {
			if f.Label == "" {
				t.Errorf("field %s has no label", f.Key)
			}
		}
		if caps.PlaceholderStyle == "" || caps.Dialect == "" {
			t.Error("capabilities must declare placeholder style and dialect")
		}
	})

	t.Run("ping", func(t *testing.T) {
		info, err := open(t, h.Values).Ping(t.Context())
		if err != nil || info.Version == "" {
			t.Fatalf("ping: %+v %v", info, err)
		}
	})

	t.Run("types", func(t *testing.T) {
		c := open(t, h.Values)
		cols, rows, _, err := query(t, c, "SELECT * FROM rb_types ORDER BY id", readOnly)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || len(cols) != len(h.Types)+1 {
			t.Fatalf("got %d rows and %d columns", len(rows), len(cols))
		}
		for i, want := range h.Types {
			col := cols[i+1]
			if col.Name != want.Name || col.Type != want.Type || col.WithTimeZone != want.WithTimeZone {
				t.Errorf("column %d: got %+v, want %s %s tz=%v", i+1, col, want.Name, want.Type, want.WithTimeZone)
			}
			got := rows[0][i+1]
			if !equal(got, want.Value) {
				t.Errorf("%s: got %#v (%T), want %#v (%T)", want.Name, got, got, want.Value, want.Value)
			}
			if rows[1][i+1] != nil {
				t.Errorf("%s: NULL came back as %#v", want.Name, rows[1][i+1])
			}
		}
	})

	t.Run("schema", func(t *testing.T) {
		s, err := open(t, h.Values).Schema(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(s.Tables, func(tb plugin.Table) bool { return tb.Name == "rb_types" })
		if i < 0 {
			t.Fatalf("rb_types not in schema (%d tables)", len(s.Tables))
		}
		tb := s.Tables[i]
		if tb.Kind != "table" || len(tb.Columns) != len(h.Types)+1 {
			t.Fatalf("rb_types: %+v", tb)
		}
		for j, want := range h.Types {
			if c := tb.Columns[j+1]; c.Name != want.Name || c.Type != want.Type || c.DBType == "" {
				t.Errorf("schema column %d: %+v, want %s %s", j+1, c, want.Name, want.Type)
			}
		}
	})

	t.Run("parameters are bound", func(t *testing.T) {
		tricky := "o'reilly\"; DROP TABLE rb_rows; --"
		_, rows, _, err := query(t, open(t, h.Values), h.ParamSQL, readOnly, tricky)
		if err != nil || len(rows) != 1 || rows[0][0] != tricky {
			t.Fatalf("got %v %v", rows, err)
		}
	})

	t.Run("row limit", func(t *testing.T) {
		c := open(t, h.Values)
		for _, tc := range []struct {
			max       int
			rows      int
			truncated bool
		}{{3, 3, true}, {10, 10, false}, {11, 10, false}} {
			opts := readOnly
			opts.MaxRows = tc.max
			_, rows, truncated, err := query(t, c, "SELECT n FROM rb_rows ORDER BY n", opts)
			if err != nil || len(rows) != tc.rows || truncated != tc.truncated {
				t.Errorf("max %d: %d rows, truncated=%v, err=%v", tc.max, len(rows), truncated, err)
			}
		}
	})

	t.Run("multiple statements are rejected", func(t *testing.T) {
		_, _, _, err := query(t, open(t, h.Values), "SELECT 1; SELECT 2", readOnly)
		wantCode(t, err, plugin.ErrCodeMultiStatement)
	})

	t.Run("multiple statements when allowed", func(t *testing.T) {
		if !caps.MultiStatement {
			t.Skip("the connector does not support several statements")
		}
		opts := readOnly
		opts.AllowMultiStatement = true
		if _, _, _, err := query(t, open(t, h.Values), "SELECT 1; SELECT 2", opts); err != nil {
			t.Fatalf("allowed multi-statement query failed: %v", err)
		}
	})

	t.Run("read-only", func(t *testing.T) {
		if !caps.ReadOnlyTx {
			t.Skip("the connector relies on database permissions (documented)")
		}
		c := open(t, h.Values)
		_, _, _, err := query(t, c, h.InsertSQL, readOnly)
		wantCode(t, err, plugin.ErrCodeReadOnlyViolation)
		_, rows, _, err := query(t, c, "SELECT n FROM rb_rows", readOnly)
		if err != nil || len(rows) != 10 {
			t.Fatalf("the insert went through: %d rows, %v", len(rows), err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		opts := readOnly
		opts.Timeout = time.Second
		start := time.Now()
		_, _, _, err := query(t, open(t, h.Values), h.SlowSQL, opts)
		wantCode(t, err, plugin.ErrCodeQueryTimeout)
		if d := time.Since(start); d > 8*time.Second {
			t.Errorf("timeout took %s", d)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		c := open(t, h.Values)
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(300*time.Millisecond, cancel)
		start := time.Now()
		rs, err := c.Query(ctx, plugin.BoundQuery{SQL: h.SlowSQL}, readOnly)
		if err == nil {
			for rs.Next() {
			}
			err = rs.Err()
			_ = rs.Close()
		}
		wantCode(t, err, plugin.ErrCodeQueryCancelled)
		if d := time.Since(start); d > 8*time.Second {
			t.Errorf("cancel took %s", d)
		}
		// The connection is still usable afterwards.
		if _, err := c.Ping(t.Context()); err != nil {
			t.Fatalf("ping after cancel: %v", err)
		}
	})

	t.Run("write permission check", func(t *testing.T) {
		can, err := open(t, h.Values).CanWrite(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if h.ReadOnlyValues == nil {
			if can == nil {
				t.Fatal("CanWrite returned unknown")
			}
			return
		}
		if can == nil || !*can {
			t.Errorf("admin user: CanWrite = %v", deref(can))
		}
		can, err = open(t, h.ReadOnlyValues).CanWrite(t.Context())
		if err != nil || can == nil || *can {
			t.Errorf("read-only user: CanWrite = %v, %v", deref(can), err)
		}
	})

	t.Run("error codes", func(t *testing.T) {
		for code, values := range h.Errors {
			t.Run(code, func(t *testing.T) {
				opts := h.Options
				opts.Values = values
				if opts.Dial == nil {
					opts.Dial = netx.NewDialer(netx.PolicyOpen).DialContext
				}
				ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
				defer cancel()
				c, err := h.Connector.Open(ctx, opts)
				if err == nil {
					_, err = c.Ping(ctx)
					_ = c.Close()
				}
				wantCode(t, err, code)
			})
		}
	})
}

func deref(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	ce, ok := plugin.AsConnError(err)
	if !ok || ce.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func equal(got, want any) bool {
	if gt, ok := got.(time.Time); ok {
		wt, ok := want.(time.Time)
		return ok && gt.Equal(wt)
	}
	return reflect.DeepEqual(got, want)
}

// Must fails the test on err; handy in Setup functions.
func Must(t *testing.T, err error, what ...any) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", fmt.Sprint(what...), err)
	}
}
