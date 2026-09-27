// Package postgres is the PostgreSQL connector (pgx through database/sql). Queries run in a
// READ ONLY transaction with a server-side statement_timeout.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/rowbird/rowbird/internal/connector/common"
	"github.com/rowbird/rowbird/internal/connector/sqlconn"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/sqlscan"
)

// ID is the plugin id.
const ID = "postgres"

func init() {
	plugin.Register(plugin.KindConnector, ID, func() plugin.Plugin { return connector{} })
}

//go:embed locales/*.json
var locales embed.FS

type connector struct{}

func (connector) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.postgres.name", Description: "plugin.postgres.description", Icon: "postgres", Version: "1.0.0"}
}

var tlsModes = []string{"disable", "prefer", "require", "verify-ca", "verify-full"}

func (connector) ConfigSchema() *plugin.Schema {
	s := &plugin.Schema{Fields: []plugin.Field{
		{Key: "host", Type: plugin.TypeString, Required: true, Format: "hostname", MaxLength: 255, Group: common.GroupConnection, Label: "plugin.common.host.label"},
		{Key: "port", Type: plugin.TypeInteger, Default: 5432, Minimum: plugin.Float(1), Maximum: plugin.Float(65535), Group: common.GroupConnection, Label: "plugin.common.port.label"},
		{Key: "database", Type: plugin.TypeString, Required: true, MaxLength: 255, Group: common.GroupConnection, Label: "plugin.common.database.label"},
		{Key: "user", Type: plugin.TypeString, Required: true, MaxLength: 255, Group: common.GroupConnection, Label: "plugin.common.user.label", Help: "plugin.common.user.help"},
		{Key: "password", Type: plugin.TypeString, Secret: true, Group: common.GroupConnection, Label: "plugin.common.password.label"},
		{Key: "tls_mode", Type: plugin.TypeString, Enum: tlsModes, Default: "prefer", Group: common.GroupTLS, Label: "plugin.common.tls_mode.label", Help: "plugin.postgres.tls_mode.help"},
	}}
	s = s.Extend(common.TLSFields("tls_mode", []any{"require", "verify-ca", "verify-full"})...)
	s = s.Extend(plugin.Field{Key: "connect_timeout", Type: plugin.TypeInteger, Default: 10, Minimum: plugin.Float(1), Maximum: plugin.Float(120), Group: common.GroupAdvanced, Label: "plugin.common.connect_timeout.label"})
	return s.Extend(common.SSHFields()...)
}

func (connector) Capabilities() any {
	return plugin.ConnectorCapabilities{
		ReadOnlyTx: true, ServerTimeout: true, Cancel: true, MultiStatement: true,
		PlaceholderStyle: "dollar", Dialect: string(sqlscan.Postgres), Network: true,
	}
}

func (connector) Messages() plugin.Messages {
	return plugin.MergeMessages(common.Messages(), plugin.MustLoadMessages(locales))
}

func (connector) Open(ctx context.Context, opts plugin.OpenOptions) (plugin.Conn, error) {
	v := opts.Values
	dial, tunnel, err := common.Dial(ctx, v, opts.Dial)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (plugin.Conn, error) {
		_ = tunnel.Close()
		return nil, err
	}

	host := common.Str(v, "host")
	u := url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(host, strconv.Itoa(common.Int(v, "port", 5432))),
		Path:   "/" + common.Str(v, "database"),
	}
	q := url.Values{}
	q.Set("sslmode", tlsMode(v))
	q.Set("connect_timeout", strconv.Itoa(common.Int(v, "connect_timeout", 10)))
	q.Set("application_name", "rowbird")
	u.RawQuery = q.Encode()
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		return fail(plugin.NewConnError(plugin.ErrCodeFailed, err))
	}
	cfg.User = common.Str(v, "user")
	cfg.Password = common.Str(v, "password")
	// Resolution happens in the dialer (network policy) or on the SSH host (tunnel).
	cfg.LookupFunc = func(_ context.Context, h string) ([]string, error) { return []string{h}, nil }
	cfg.DialFunc = pgconn.DialFunc(dial)
	if err := common.ApplyTLSMaterial(cfg.TLSConfig, v); err != nil {
		return fail(err)
	}
	for _, fb := range cfg.Fallbacks {
		if err := common.ApplyTLSMaterial(fb.TLSConfig, v); err != nil {
			return fail(err)
		}
	}
	// Plain queries go through the extended protocol, which also refuses multiple statements.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe

	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(3)
	db.SetConnMaxIdleTime(5 * time.Minute)
	c := &conn{Conn: sqlconn.Conn{DB: db, Driver: driver{}, Closers: []io.Closer{tunnel}, Logger: opts.Logger}}
	if _, err := c.Ping(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func tlsMode(v map[string]any) string {
	if m := common.Str(v, "tls_mode"); m != "" {
		return m
	}
	return "prefer"
}

type conn struct {
	sqlconn.Conn
}

func (c *conn) Ping(ctx context.Context) (plugin.ServerInfo, error) {
	var version string
	if err := c.DB.QueryRowContext(ctx, "SELECT current_setting('server_version')").Scan(&version); err != nil {
		return plugin.ServerInfo{}, connectError(err)
	}
	return plugin.ServerInfo{Version: "PostgreSQL " + version}, nil
}

// CanWrite checks superuser, data-changing privileges on any user table, and CREATE on the
// database or any user schema.
func (c *conn) CanWrite(ctx context.Context) (*bool, error) {
	var can bool
	err := c.DB.QueryRowContext(ctx, `
		SELECT r.rolsuper
			OR has_database_privilege(current_database(), 'CREATE')
			OR EXISTS (
				SELECT 1 FROM pg_class cl JOIN pg_namespace n ON n.oid = cl.relnamespace
				WHERE cl.relkind IN ('r', 'p') AND n.nspname NOT IN ('pg_catalog', 'information_schema')
					AND n.nspname NOT LIKE 'pg_toast%'
					AND has_table_privilege(cl.oid, 'INSERT, UPDATE, DELETE, TRUNCATE'))
			OR EXISTS (
				SELECT 1 FROM pg_namespace n
				WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg_%'
					AND has_schema_privilege(n.oid, 'CREATE'))
		FROM pg_roles r WHERE r.rolname = current_user`).Scan(&can)
	if err != nil {
		return nil, connectError(err)
	}
	return &can, nil
}

func (c *conn) Schema(ctx context.Context) (*plugin.DBSchema, error) {
	rows, err := c.DB.QueryContext(ctx, `
		SELECT n.nspname, cl.relname, cl.relkind, COALESCE(obj_description(cl.oid, 'pg_class'), ''),
			a.attname, format_type(a.atttypid, a.atttypmod), t.typname, NOT a.attnotnull,
			COALESCE(col_description(cl.oid, a.attnum), '')
		FROM pg_class cl
		JOIN pg_namespace n ON n.oid = cl.relnamespace
		JOIN pg_attribute a ON a.attrelid = cl.oid AND a.attnum > 0 AND NOT a.attisdropped
		JOIN pg_type t ON t.oid = a.atttypid
		WHERE cl.relkind IN ('r', 'v', 'm', 'p', 'f')
			AND n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg_toast%'
			AND has_table_privilege(cl.oid, 'SELECT')
		ORDER BY n.nspname, cl.relname, a.attnum`)
	if err != nil {
		return nil, connectError(err)
	}
	defer func() { _ = rows.Close() }()
	var tables []plugin.Table
	for rows.Next() {
		var schema, name, kind, comment, col, dbType, typName, colComment string
		var nullable bool
		if err := rows.Scan(&schema, &name, &kind, &comment, &col, &dbType, &typName, &nullable, &colComment); err != nil {
			return nil, connectError(err)
		}
		if n := len(tables); n == 0 || tables[n-1].Schema != schema || tables[n-1].Name != name {
			k := "table"
			if kind == "v" || kind == "m" {
				k = "view"
			}
			tables = append(tables, plugin.Table{Schema: schema, Name: name, Kind: k, Comment: comment})
		}
		t := &tables[len(tables)-1]
		vt, _ := typeOf(typName)
		t.Columns = append(t.Columns, plugin.TableColumn{Name: col, Type: vt, DBType: dbType, Nullable: nullable, Comment: colComment})
	}
	return &plugin.DBSchema{Tables: tables}, rows.Err()
}

// connectError classifies errors raised while connecting.
func connectError(err error) error {
	if ce, ok := plugin.AsConnError(err); ok {
		return ce
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "28P01", pgErr.Code == "28000" && !strings.Contains(pgErr.Message, "no encryption"):
			return plugin.NewConnError(plugin.ErrCodeAuthFailed, err)
		case pgErr.Code == "28000":
			return plugin.NewConnError(plugin.ErrCodeTLSRequired, err)
		case pgErr.Code == "3D000":
			return plugin.NewConnError(plugin.ErrCodeDatabaseNotFound, err)
		}
	}
	msg := err.Error()
	var netErr net.Error
	switch {
	case errors.Is(err, netx.ErrBlocked):
		return plugin.NewConnError(plugin.ErrCodeNetworkBlocked, err)
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout(), strings.Contains(msg, "timeout"):
		return plugin.NewConnError(plugin.ErrCodeTimeout, err)
	case strings.Contains(msg, "x509:") || strings.Contains(msg, "tls:"), strings.Contains(msg, "server refused TLS"):
		return plugin.NewConnError(plugin.ErrCodeTLSFailed, err)
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"), strings.Contains(msg, "dial"):
		return plugin.NewConnError(plugin.ErrCodeHostUnreachable, err)
	}
	return plugin.NewConnError(plugin.ErrCodeFailed, err)
}

type driver struct{}

func (driver) Dialect() sqlscan.Dialect { return sqlscan.Postgres }

// TxOptions always opens a transaction so SET LOCAL applies only to this query.
func (driver) TxOptions(opts plugin.QueryOptions) *sql.TxOptions {
	return &sql.TxOptions{ReadOnly: opts.ReadOnly}
}

func (driver) SessionStatements(opts plugin.QueryOptions) []string {
	if opts.Timeout <= 0 {
		return nil
	}
	return []string{fmt.Sprintf("SET LOCAL statement_timeout = %d", opts.Timeout.Milliseconds())}
}

// typeOf maps pg_type.typname; the second result is true for types with a time zone.
func typeOf(typName string) (plugin.ValueType, bool) {
	switch strings.ToLower(strings.TrimPrefix(typName, "_")) {
	case "int2", "int4", "int8", "oid":
		return plugin.TypeInt, false
	case "numeric", "money":
		return plugin.TypeDecimal, false
	case "float4", "float8":
		return plugin.TypeFloat, false
	case "bool":
		return plugin.TypeBool, false
	case "date":
		return plugin.TypeDate, false
	case "timestamp":
		return plugin.TypeDateTime, false
	case "timestamptz":
		return plugin.TypeDateTime, true
	case "time", "timetz":
		return plugin.TypeTime, false
	case "json", "jsonb":
		return plugin.TypeJSON, false
	case "bytea":
		return plugin.TypeBinary, false
	case "text", "varchar", "bpchar", "char", "name", "uuid", "citext", "inet", "cidr", "macaddr", "interval", "xml":
		return plugin.TypeText, false
	}
	return plugin.TypeUnknown, false
}

func (driver) Column(ct *sql.ColumnType) plugin.Column {
	name := ct.DatabaseTypeName()
	t, tz := typeOf(name)
	if strings.HasPrefix(name, "_") {
		t = plugin.TypeText // arrays are shown as text
	}
	return plugin.Column{Name: ct.Name(), Type: t, DBType: strings.ToLower(name), WithTimeZone: tz}
}

func (driver) Convert(col plugin.Column, v any) (any, error) {
	switch col.Type {
	case plugin.TypeInt:
		switch x := v.(type) {
		case int64:
			return x, nil
		case int32:
			return int64(x), nil
		case int16:
			return int64(x), nil
		case uint32:
			return int64(x), nil
		}
	case plugin.TypeDecimal:
		return plugin.Decimal(asString(v)), nil
	case plugin.TypeFloat:
		switch x := v.(type) {
		case float64:
			return x, nil
		case float32:
			return float64(x), nil
		}
	case plugin.TypeBool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	case plugin.TypeDate:
		if t, ok := v.(time.Time); ok {
			return plugin.Date(t.Format(time.DateOnly)), nil
		}
	case plugin.TypeDateTime:
		if t, ok := v.(time.Time); ok {
			return t.UTC(), nil
		}
	case plugin.TypeTime:
		return plugin.TimeOfDay(asString(v)), nil
	case plugin.TypeJSON:
		return plugin.JSON(asString(v)), nil
	case plugin.TypeBinary:
		if b, ok := v.([]byte); ok {
			return b, nil
		}
	}
	return asString(v), nil
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}

// AdjustArgs switches to the simple protocol only when the connection allows several statements;
// the default extended protocol refuses them at the server as a second line of defense.
func (driver) AdjustArgs(opts plugin.QueryOptions, args []any) []any {
	if !opts.AllowMultiStatement {
		return args
	}
	return append([]any{pgx.QueryExecModeSimpleProtocol}, args...)
}

func (driver) QueryError(err error) *plugin.ConnError {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil
	}
	switch pgErr.Code {
	case "25006":
		return plugin.NewConnError(plugin.ErrCodeReadOnlyViolation, err)
	case "57014":
		if strings.Contains(pgErr.Message, "statement timeout") {
			return plugin.NewConnError(plugin.ErrCodeQueryTimeout, err)
		}
		return plugin.NewConnError(plugin.ErrCodeQueryCancelled, err)
	case "42601":
		if strings.Contains(pgErr.Message, "multiple commands") {
			return plugin.NewConnError(plugin.ErrCodeMultiStatement, err)
		}
	}
	return nil
}
