// Package sqlite is the SQLite connector. Files are opened read-only, and only inside the
// directories allowed by ROWBIRD_SQLITE_DIRS; Rowbird's own store is never allowed.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/rowbird/rowbird/internal/connector/common"
	"github.com/rowbird/rowbird/internal/connector/sqlconn"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/sqlscan"
)

// ID is the plugin id.
const ID = "sqlite"

func init() {
	plugin.Register(plugin.KindConnector, ID, func() plugin.Plugin { return connector{} })
}

type connector struct{}

func (connector) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.sqlite.name", Description: "plugin.sqlite.description", Icon: "sqlite", Version: "1.0.0"}
}

func (connector) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "path", Type: plugin.TypeString, Required: true, Format: "path", MaxLength: 1024, Group: common.GroupConnection, Label: "plugin.sqlite.path.label", Help: "plugin.sqlite.path.help"},
	}}
}

func (connector) Capabilities() any {
	return plugin.ConnectorCapabilities{
		ReadOnlyTx: true, ServerTimeout: false, Cancel: true, MultiStatement: false,
		PlaceholderStyle: "question", Dialect: string(sqlscan.SQLite), Network: false,
	}
}

//go:embed locales/*.json
var locales embed.FS

func (connector) Messages() plugin.Messages {
	return plugin.MergeMessages(common.Messages(), plugin.MustLoadMessages(locales))
}

// Open checks the path against the allowed directories and opens it read-only.
func (connector) Open(ctx context.Context, opts plugin.OpenOptions) (plugin.Conn, error) {
	path, err := allowedPath(common.Str(opts.Values, "path"), opts.SQLiteDirs, opts.ForbiddenPaths)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	dsn := u.String() + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, plugin.NewConnError(plugin.ErrCodeFailed, err)
	}
	db.SetMaxOpenConns(2)
	c := &conn{Conn: sqlconn.Conn{DB: db, Driver: driver{}, Logger: opts.Logger}}
	if _, err := c.Ping(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return c, nil
}

// allowedPath resolves symlinks on both sides so a link inside an allowed directory cannot point
// outside it.
func allowedPath(p string, dirs, forbidden []string) (string, error) {
	if p == "" || !filepath.IsAbs(p) {
		return "", plugin.NewConnError(plugin.ErrCodePathNotAllowed, errors.New("sqlite: path must be absolute"))
	}
	real, err := filepath.EvalSymlinks(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", plugin.NewConnError(plugin.ErrCodeDatabaseNotFound, err)
	}
	if err != nil {
		return "", plugin.NewConnError(plugin.ErrCodePathNotAllowed, err)
	}
	for _, f := range forbidden {
		if fr, err := filepath.EvalSymlinks(f); err == nil && fr == real {
			return "", plugin.NewConnError(plugin.ErrCodePathNotAllowed, errors.New("sqlite: the internal store cannot be used as a connection"))
		}
	}
	for _, d := range dirs {
		dr, err := filepath.EvalSymlinks(d)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(dr, real); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "." {
			if info, err := os.Stat(real); err != nil || info.IsDir() {
				return "", plugin.NewConnError(plugin.ErrCodeDatabaseNotFound, errors.New("sqlite: not a file"))
			}
			return real, nil
		}
	}
	return "", plugin.NewConnError(plugin.ErrCodePathNotAllowed, fmt.Errorf("sqlite: %s is outside the allowed directories", p))
}

type conn struct {
	sqlconn.Conn
}

func (c *conn) Ping(ctx context.Context) (plugin.ServerInfo, error) {
	var v string
	if err := c.DB.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&v); err != nil {
		return plugin.ServerInfo{}, openError(err)
	}
	return plugin.ServerInfo{Version: "SQLite " + v}, nil
}

// CanWrite is always false: the file is opened read-only.
func (c *conn) CanWrite(context.Context) (*bool, error) {
	f := false
	return &f, nil
}

func (c *conn) Schema(ctx context.Context) (*plugin.DBSchema, error) {
	rows, err := c.DB.QueryContext(ctx, `SELECT name, type FROM sqlite_schema WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, openError(err)
	}
	var tables []plugin.Table
	for rows.Next() {
		var t plugin.Table
		if err := rows.Scan(&t.Name, &t.Kind); err != nil {
			_ = rows.Close()
			return nil, openError(err)
		}
		tables = append(tables, t)
	}
	_ = rows.Close()
	for i := range tables {
		cols, err := c.DB.QueryContext(ctx, `SELECT name, type, "notnull" FROM pragma_table_info(?) ORDER BY cid`, tables[i].Name)
		if err != nil {
			return nil, openError(err)
		}
		for cols.Next() {
			var col plugin.TableColumn
			var notNull int
			if err := cols.Scan(&col.Name, &col.DBType, &notNull); err != nil {
				_ = cols.Close()
				return nil, openError(err)
			}
			col.Nullable = notNull == 0
			col.Type = typeOf(col.DBType)
			if col.Type == "" {
				col.Type = plugin.TypeUnknown
			}
			tables[i].Columns = append(tables[i].Columns, col)
		}
		_ = cols.Close()
	}
	return &plugin.DBSchema{Tables: tables}, nil
}

func (c *conn) Close() error { return c.Conn.Close() }

func openError(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) && (se.Code() == sqlite3.SQLITE_NOTADB || se.Code() == sqlite3.SQLITE_CANTOPEN) {
		return plugin.NewConnError(plugin.ErrCodeDatabaseNotFound, err)
	}
	return plugin.NewConnError(plugin.ErrCodeFailed, err)
}

type driver struct{}

func (driver) Dialect() sqlscan.Dialect { return sqlscan.SQLite }

// TxOptions: none needed, the connection itself is read-only (mode=ro, query_only).
func (driver) TxOptions(plugin.QueryOptions) *sql.TxOptions { return nil }

func (driver) SessionStatements(plugin.QueryOptions) []string { return nil }

// typeOf maps a declared type by SQLite affinity rules; "" means dynamic (expressions).
func typeOf(decl string) plugin.ValueType {
	d := strings.ToUpper(decl)
	switch {
	case d == "":
		return ""
	case strings.Contains(d, "BOOL"):
		return plugin.TypeBool
	case strings.Contains(d, "DATETIME") || strings.Contains(d, "TIMESTAMP"):
		return plugin.TypeDateTime
	case d == "DATE":
		return plugin.TypeDate
	case d == "TIME":
		return plugin.TypeTime
	case d == "JSON":
		return plugin.TypeJSON
	case strings.Contains(d, "INT"):
		return plugin.TypeInt
	case strings.Contains(d, "CHAR") || strings.Contains(d, "CLOB") || strings.Contains(d, "TEXT"):
		return plugin.TypeText
	case strings.Contains(d, "BLOB"):
		return plugin.TypeBinary
	case strings.Contains(d, "REAL") || strings.Contains(d, "FLOA") || strings.Contains(d, "DOUBLE"):
		return plugin.TypeFloat
	case strings.Contains(d, "DEC") || strings.Contains(d, "NUMERIC"):
		return plugin.TypeDecimal
	}
	return plugin.TypeUnknown
}

func (driver) Column(ct *sql.ColumnType) plugin.Column {
	return plugin.Column{Name: ct.Name(), Type: typeOf(ct.DatabaseTypeName()), DBType: ct.DatabaseTypeName()}
}

// Convert normalizes values. SQLite stores DECIMAL columns with REAL or INTEGER affinity, so the
// decimal text is the shortest exact rendering of what SQLite holds, not the declared precision.
func (driver) Convert(col plugin.Column, v any) (any, error) {
	switch col.Type {
	case plugin.TypeInt:
		switch x := v.(type) {
		case int64:
			return x, nil
		case float64:
			return int64(x), nil
		}
	case plugin.TypeFloat:
		switch x := v.(type) {
		case float64:
			return x, nil
		case int64:
			return float64(x), nil
		}
	case plugin.TypeDecimal:
		switch x := v.(type) {
		case int64:
			return plugin.Decimal(strconv.FormatInt(x, 10)), nil
		case float64:
			return plugin.Decimal(strconv.FormatFloat(x, 'f', -1, 64)), nil
		case string:
			return plugin.Decimal(x), nil
		}
	case plugin.TypeBool:
		switch x := v.(type) {
		case int64:
			return x != 0, nil
		case bool:
			return x, nil
		}
	case plugin.TypeDate:
		if t, ok := v.(time.Time); ok {
			return plugin.Date(t.Format(time.DateOnly)), nil
		}
		if s, ok := v.(string); ok {
			return plugin.Date(s), nil
		}
	case plugin.TypeDateTime:
		if t, ok := v.(time.Time); ok {
			return t.UTC(), nil
		}
	case plugin.TypeTime:
		if s, ok := v.(string); ok {
			return plugin.TimeOfDay(s), nil
		}
	case plugin.TypeJSON:
		if s, ok := v.(string); ok {
			return plugin.JSON(s), nil
		}
	case plugin.TypeBinary:
		if b, ok := v.([]byte); ok {
			return b, nil
		}
	case plugin.TypeText:
		switch x := v.(type) {
		case string:
			return x, nil
		case []byte:
			return string(x), nil
		}
	}
	// Anything else (dynamic typing lets a TEXT column hold a number) is shown as text.
	return fmt.Sprint(v), nil
}

func (driver) QueryError(err error) *plugin.ConnError {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return nil
	}
	switch se.Code() & 0xff {
	case sqlite3.SQLITE_READONLY:
		return plugin.NewConnError(plugin.ErrCodeReadOnlyViolation, err)
	case sqlite3.SQLITE_INTERRUPT:
		return plugin.NewConnError(plugin.ErrCodeQueryCancelled, err)
	}
	return nil
}
