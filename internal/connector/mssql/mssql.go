// Package mssql is the SQL Server connector (microsoft/go-mssqldb). SQL Server has no read-only
// transactions for this purpose, so safety relies on a read-only database user, which the UI
// insists on (ADR-0013). Timeouts and cancel send a TDS attention that stops the batch on the server.
package mssql

import (
	"context"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	mssqldb "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"

	"github.com/rowbird/rowbird/internal/connector/common"
	"github.com/rowbird/rowbird/internal/connector/sqlconn"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/sqlscan"
)

// ID is the plugin id.
const ID = "mssql"

func init() {
	plugin.Register(plugin.KindConnector, ID, func() plugin.Plugin { return connector{} })
}

//go:embed locales/*.json
var locales embed.FS

type connector struct{}

func (connector) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.mssql.name", Description: "plugin.mssql.description", Icon: "mssql", Version: "1.0.0"}
}

// TLS modes mapped to go-mssqldb's encrypt and TrustServerCertificate settings.
var tlsModes = []string{"disable", "prefer", "require", "verify-full", "strict"}

func (connector) ConfigSchema() *plugin.Schema {
	s := &plugin.Schema{Fields: []plugin.Field{
		{Key: "host", Type: plugin.TypeString, Required: true, Format: "hostname", MaxLength: 255, Group: common.GroupConnection, Label: "plugin.common.host.label"},
		{Key: "port", Type: plugin.TypeInteger, Default: 1433, Minimum: plugin.Float(1), Maximum: plugin.Float(65535), Group: common.GroupConnection, Label: "plugin.common.port.label"},
		{Key: "database", Type: plugin.TypeString, Required: true, MaxLength: 255, Group: common.GroupConnection, Label: "plugin.common.database.label"},
		{Key: "user", Type: plugin.TypeString, Required: true, MaxLength: 255, Group: common.GroupConnection, Label: "plugin.common.user.label", Help: "plugin.mssql.user.help"},
		{Key: "password", Type: plugin.TypeString, Secret: true, Group: common.GroupConnection, Label: "plugin.common.password.label"},
		{Key: "tls_mode", Type: plugin.TypeString, Enum: tlsModes, Default: "require", Group: common.GroupTLS, Label: "plugin.common.tls_mode.label", Help: "plugin.mssql.tls_mode.help"},
		{Key: "tls_ca", Type: plugin.TypeString, Multiline: true, Format: "pem", Group: common.GroupTLS, ShowIf: &plugin.ShowIf{Field: "tls_mode", In: []any{"verify-full", "strict"}}, Label: "plugin.common.tls_ca.label", Help: "plugin.common.tls_ca.help"},
		{Key: "connect_timeout", Type: plugin.TypeInteger, Default: 15, Minimum: plugin.Float(1), Maximum: plugin.Float(120), Group: common.GroupAdvanced, Label: "plugin.common.connect_timeout.label"},
	}}
	return s.Extend(common.SSHFields()...)
}

func (connector) Capabilities() any {
	return plugin.ConnectorCapabilities{
		ReadOnlyTx: false, ServerTimeout: false, Cancel: true, MultiStatement: true,
		PlaceholderStyle: "at", Dialect: string(sqlscan.MSSQL), Network: true,
	}
}

func (connector) Messages() plugin.Messages {
	return plugin.MergeMessages(common.Messages(), plugin.MustLoadMessages(locales))
}

// hostDialer keeps DNS resolution in our dialer (network policy) or on the SSH host: go-mssqldb
// only skips its own lookup for dialers that implement HostName.
type hostDialer struct {
	dial plugin.DialFunc
	host string
}

func (d hostDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.dial(ctx, network, addr)
}

func (d hostDialer) HostName() string { return d.host }

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
	q := url.Values{}
	q.Set("database", common.Str(v, "database"))
	q.Set("app name", "rowbird")
	q.Set("dial timeout", strconv.Itoa(common.Int(v, "connect_timeout", 15)))
	q.Set("connection timeout", strconv.Itoa(common.Int(v, "connect_timeout", 15)))
	switch mode := common.Str(v, "tls_mode"); mode {
	case "disable":
		q.Set("encrypt", "disable")
	case "prefer":
		q.Set("encrypt", "false") // login packet only, as the server allows
	case "", "require":
		q.Set("encrypt", "true")
		q.Set("TrustServerCertificate", "true")
	case "verify-full":
		q.Set("encrypt", "true")
	case "strict":
		q.Set("encrypt", "strict")
	}
	u := url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(common.Str(v, "user"), common.Str(v, "password")),
		Host:     net.JoinHostPort(host, strconv.Itoa(common.Int(v, "port", 1433))),
		RawQuery: q.Encode(),
	}
	cfg, err := msdsn.Parse(u.String())
	if err != nil {
		return fail(plugin.NewConnError(plugin.ErrCodeFailed, errors.New("mssql: invalid connection settings")))
	}
	cfg.DisableRetry = true
	if err := common.ApplyTLSMaterial(cfg.TLSConfig, v); err != nil {
		return fail(err)
	}
	conn := mssqldb.NewConnectorConfig(cfg)
	conn.Dialer = hostDialer{dial: dial, host: host}

	db := sql.OpenDB(conn)
	db.SetMaxOpenConns(3)
	db.SetConnMaxIdleTime(5 * time.Minute)
	c := &sqlServerConn{Conn: sqlconn.Conn{DB: db, Driver: driver{}, Closers: []io.Closer{tunnel}, Logger: opts.Logger}}
	if _, err := c.Ping(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

type sqlServerConn struct {
	sqlconn.Conn
}

func (c *sqlServerConn) Ping(ctx context.Context) (plugin.ServerInfo, error) {
	var version, edition string
	err := c.DB.QueryRowContext(ctx, "SELECT CAST(SERVERPROPERTY('ProductVersion') AS nvarchar(128)), CAST(SERVERPROPERTY('Edition') AS nvarchar(128))").Scan(&version, &edition)
	if err != nil {
		return plugin.ServerInfo{}, connectError(err)
	}
	return plugin.ServerInfo{Version: fmt.Sprintf("SQL Server %s (%s)", version, edition)}, nil
}

// CanWrite checks roles and database-level permissions. Permissions granted on single tables are
// not inspected (best effort, docs/spec/03-flows.md).
func (c *sqlServerConn) CanWrite(ctx context.Context) (*bool, error) {
	var can int
	err := c.DB.QueryRowContext(ctx, `
		SELECT CASE WHEN IS_SRVROLEMEMBER('sysadmin') = 1 OR IS_ROLEMEMBER('db_owner') = 1
			OR IS_ROLEMEMBER('db_datawriter') = 1 OR IS_ROLEMEMBER('db_ddladmin') = 1
			OR HAS_PERMS_BY_NAME(DB_NAME(), 'DATABASE', 'INSERT') = 1
			OR HAS_PERMS_BY_NAME(DB_NAME(), 'DATABASE', 'UPDATE') = 1
			OR HAS_PERMS_BY_NAME(DB_NAME(), 'DATABASE', 'DELETE') = 1
			OR HAS_PERMS_BY_NAME(DB_NAME(), 'DATABASE', 'CREATE TABLE') = 1
		THEN 1 ELSE 0 END`).Scan(&can)
	if err != nil {
		return nil, connectError(err)
	}
	b := can == 1
	return &b, nil
}

func (c *sqlServerConn) Schema(ctx context.Context) (*plugin.DBSchema, error) {
	rows, err := c.DB.QueryContext(ctx, `
		SELECT s.name, o.name, o.type, COALESCE(CAST(ep.value AS nvarchar(4000)), ''),
			c.name, ty.name, c.max_length, c.precision, c.scale, c.is_nullable,
			COALESCE(CAST(epc.value AS nvarchar(4000)), '')
		FROM sys.objects o
		JOIN sys.schemas s ON s.schema_id = o.schema_id
		JOIN sys.columns c ON c.object_id = o.object_id
		JOIN sys.types ty ON ty.user_type_id = c.user_type_id
		LEFT JOIN sys.extended_properties ep ON ep.major_id = o.object_id AND ep.minor_id = 0 AND ep.class = 1 AND ep.name = 'MS_Description'
		LEFT JOIN sys.extended_properties epc ON epc.major_id = o.object_id AND epc.minor_id = c.column_id AND epc.class = 1 AND epc.name = 'MS_Description'
		WHERE o.type IN ('U', 'V') AND o.is_ms_shipped = 0
		ORDER BY s.name, o.name, c.column_id`)
	if err != nil {
		return nil, connectError(err)
	}
	defer func() { _ = rows.Close() }()
	var tables []plugin.Table
	for rows.Next() {
		var schema, name, kind, comment, col, typ, colComment string
		var maxLen int16
		var precision, scale uint8
		var nullable bool
		if err := rows.Scan(&schema, &name, &kind, &comment, &col, &typ, &maxLen, &precision, &scale, &nullable, &colComment); err != nil {
			return nil, connectError(err)
		}
		if n := len(tables); n == 0 || tables[n-1].Schema != schema || tables[n-1].Name != name {
			k := "table"
			if strings.TrimSpace(kind) == "V" {
				k = "view"
			}
			tables = append(tables, plugin.Table{Schema: schema, Name: name, Kind: k, Comment: comment})
		}
		t := &tables[len(tables)-1]
		vt, _ := typeOf(typ)
		t.Columns = append(t.Columns, plugin.TableColumn{Name: col, Type: vt, DBType: dbType(typ, maxLen, precision, scale), Nullable: nullable, Comment: colComment})
	}
	return &plugin.DBSchema{Tables: tables}, rows.Err()
}

func dbType(typ string, maxLen int16, precision, scale uint8) string {
	switch typ {
	case "decimal", "numeric":
		return fmt.Sprintf("%s(%d,%d)", typ, precision, scale)
	case "varchar", "char", "varbinary", "binary":
		if maxLen < 0 {
			return typ + "(max)"
		}
		return fmt.Sprintf("%s(%d)", typ, maxLen)
	case "nvarchar", "nchar":
		if maxLen < 0 {
			return typ + "(max)"
		}
		return fmt.Sprintf("%s(%d)", typ, maxLen/2)
	}
	return typ
}

func connectError(err error) error {
	if ce, ok := plugin.AsConnError(err); ok {
		return ce
	}
	var me mssqldb.Error
	if errors.As(err, &me) {
		switch me.Number {
		case 18456:
			return plugin.NewConnError(plugin.ErrCodeAuthFailed, err)
		case 4060, 4063:
			// 4060: cannot open the database; 4063: the same, when the login has a default database
			// to fall back to (master, for example).
			return plugin.NewConnError(plugin.ErrCodeDatabaseNotFound, err)
		}
	}
	msg := err.Error()
	var netErr net.Error
	switch {
	case errors.Is(err, netx.ErrBlocked):
		return plugin.NewConnError(plugin.ErrCodeNetworkBlocked, err)
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return plugin.NewConnError(plugin.ErrCodeTimeout, err)
	case strings.Contains(msg, "x509:") || strings.Contains(msg, "TLS Handshake failed") || strings.Contains(msg, "tls:"):
		return plugin.NewConnError(plugin.ErrCodeTLSFailed, err)
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"), strings.Contains(msg, "unable to open tcp connection"):
		return plugin.NewConnError(plugin.ErrCodeHostUnreachable, err)
	}
	return plugin.NewConnError(plugin.ErrCodeFailed, err)
}

type driver struct{}

func (driver) Dialect() sqlscan.Dialect { return sqlscan.MSSQL }

// TxOptions: no transaction; SQL Server cannot make one read-only, so a read-only user is required.
func (driver) TxOptions(plugin.QueryOptions) *sql.TxOptions { return nil }

func (driver) SessionStatements(opts plugin.QueryOptions) []string {
	if opts.Timeout <= 0 {
		return nil
	}
	return []string{fmt.Sprintf("SET LOCK_TIMEOUT %d", opts.Timeout.Milliseconds())}
}

func typeOf(name string) (plugin.ValueType, bool) {
	switch strings.ToUpper(name) {
	case "INT", "BIGINT", "SMALLINT", "TINYINT":
		return plugin.TypeInt, false
	case "DECIMAL", "NUMERIC", "MONEY", "SMALLMONEY":
		return plugin.TypeDecimal, false
	case "FLOAT", "REAL":
		return plugin.TypeFloat, false
	case "BIT":
		return plugin.TypeBool, false
	case "DATE":
		return plugin.TypeDate, false
	case "DATETIME", "DATETIME2", "SMALLDATETIME":
		return plugin.TypeDateTime, false
	case "DATETIMEOFFSET":
		return plugin.TypeDateTime, true
	case "TIME":
		return plugin.TypeTime, false
	case "VARBINARY", "BINARY", "IMAGE", "TIMESTAMP", "ROWVERSION":
		return plugin.TypeBinary, false
	case "VARCHAR", "NVARCHAR", "CHAR", "NCHAR", "TEXT", "NTEXT", "UNIQUEIDENTIFIER", "XML", "SYSNAME":
		return plugin.TypeText, false
	}
	return plugin.TypeUnknown, false
}

func (driver) Column(ct *sql.ColumnType) plugin.Column {
	name := ct.DatabaseTypeName()
	t, tz := typeOf(name)
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
		case uint8:
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
		if t, ok := v.(time.Time); ok {
			return plugin.TimeOfDay(t.Format("15:04:05.999999999")), nil
		}
	case plugin.TypeBinary:
		if b, ok := v.([]byte); ok {
			return b, nil
		}
	case plugin.TypeText:
		if b, ok := v.([]byte); ok && strings.EqualFold(col.DBType, "uniqueidentifier") && len(b) == 16 {
			return formatGUID(b), nil
		}
	}
	return asString(v), nil
}

// formatGUID renders SQL Server's mixed-endian uniqueidentifier bytes as the usual text form.
func formatGUID(b []byte) string {
	g := []byte{b[3], b[2], b[1], b[0], b[5], b[4], b[7], b[6], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15]}
	h := strings.ToUpper(hex.EncodeToString(g))
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func asString(v any) string {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(v)
}

func (driver) QueryError(err error) *plugin.ConnError {
	var me mssqldb.Error
	if errors.As(err, &me) && me.Number == 1222 { // lock request time out
		return plugin.NewConnError(plugin.ErrCodeQueryTimeout, err)
	}
	return nil
}
