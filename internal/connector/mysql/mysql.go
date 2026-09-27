// Package mysql is the MySQL and MariaDB connector (go-sql-driver/mysql). Queries run in a
// READ ONLY transaction with a server-side execution time limit (max_execution_time on MySQL,
// max_statement_time on MariaDB). Cancelling kills the statement on the server.
package mysql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	mysqldrv "github.com/go-sql-driver/mysql"

	"github.com/rowbird/rowbird/internal/connector/common"
	"github.com/rowbird/rowbird/internal/connector/sqlconn"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/sqlscan"
)

// ID is the plugin id.
const ID = "mysql"

func init() {
	plugin.Register(plugin.KindConnector, ID, func() plugin.Plugin { return connector{} })
}

//go:embed locales/*.json
var locales embed.FS

type connector struct{}

func (connector) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.mysql.name", Description: "plugin.mysql.description", Icon: "mysql", Version: "1.0.0"}
}

var tlsModes = []string{"disable", "prefer", "require", "verify-ca", "verify-full"}

func (connector) ConfigSchema() *plugin.Schema {
	s := &plugin.Schema{Fields: []plugin.Field{
		{Key: "host", Type: plugin.TypeString, Required: true, Format: "hostname", MaxLength: 255, Group: common.GroupConnection, Label: "plugin.common.host.label"},
		{Key: "port", Type: plugin.TypeInteger, Default: 3306, Minimum: plugin.Float(1), Maximum: plugin.Float(65535), Group: common.GroupConnection, Label: "plugin.common.port.label"},
		{Key: "database", Type: plugin.TypeString, Required: true, MaxLength: 255, Group: common.GroupConnection, Label: "plugin.common.database.label"},
		{Key: "user", Type: plugin.TypeString, Required: true, MaxLength: 255, Group: common.GroupConnection, Label: "plugin.common.user.label", Help: "plugin.common.user.help"},
		{Key: "password", Type: plugin.TypeString, Secret: true, Group: common.GroupConnection, Label: "plugin.common.password.label"},
		{Key: "tls_mode", Type: plugin.TypeString, Enum: tlsModes, Default: "prefer", Group: common.GroupTLS, Label: "plugin.common.tls_mode.label", Help: "plugin.mysql.tls_mode.help"},
	}}
	s = s.Extend(common.TLSFields("tls_mode", []any{"require", "verify-ca", "verify-full"})...)
	s = s.Extend(plugin.Field{Key: "connect_timeout", Type: plugin.TypeInteger, Default: 10, Minimum: plugin.Float(1), Maximum: plugin.Float(120), Group: common.GroupAdvanced, Label: "plugin.common.connect_timeout.label"})
	return s.Extend(common.SSHFields()...)
}

func (connector) Capabilities() any {
	return plugin.ConnectorCapabilities{
		ReadOnlyTx: true, ServerTimeout: true, Cancel: true, MultiStatement: true,
		PlaceholderStyle: "question", Dialect: string(sqlscan.MySQL), Network: true,
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
	cfg := mysqldrv.NewConfig()
	cfg.Net = "tcp"
	host := common.Str(v, "host")
	cfg.Addr = net.JoinHostPort(host, strconv.Itoa(common.Int(v, "port", 3306)))
	cfg.DBName = common.Str(v, "database")
	cfg.User = common.Str(v, "user")
	cfg.Passwd = common.Str(v, "password")
	cfg.DialFunc = dial
	cfg.Timeout = time.Duration(common.Int(v, "connect_timeout", 10)) * time.Second
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Params = map[string]string{"time_zone": "'+00:00'"}
	if err := applyTLS(cfg, host, v); err != nil {
		_ = tunnel.Close()
		return nil, err
	}

	connector, err := mysqldrv.NewConnector(cfg)
	if err != nil {
		_ = tunnel.Close()
		return nil, plugin.NewConnError(plugin.ErrCodeFailed, err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)

	c := &conn{}
	c.Conn = sqlconn.Conn{DB: db, Driver: &c.drv, Closers: []io.Closer{tunnel, closerFunc(c.closeMulti)}, Logger: opts.Logger}
	c.MultiDB = func() (*sql.DB, error) { return c.multi(cfg) }
	info, err := c.Ping(ctx)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	c.drv.mariadb = strings.Contains(info.Version, "MariaDB")
	return c, nil
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func applyTLS(cfg *mysqldrv.Config, host string, v map[string]any) error {
	mode := common.Str(v, "tls_mode")
	if mode == "" {
		mode = "prefer"
	}
	t := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	switch mode {
	case "disable":
		return nil
	case "prefer":
		t.InsecureSkipVerify = true //nolint:gosec // opportunistic encryption, as in libpq's prefer
		cfg.AllowFallbackToPlaintext = true
	case "require":
		t.InsecureSkipVerify = true //nolint:gosec // encryption without verification is what "require" means
	case "verify-ca":
		t.InsecureSkipVerify = true //nolint:gosec // the chain is verified below, only the host name is not
		// VerifyConnection also runs on resumed sessions, unlike VerifyPeerCertificate.
		t.VerifyConnection = func(cs tls.ConnectionState) error { return verifyChain(t, cs.PeerCertificates) }
	}
	if err := common.ApplyTLSMaterial(t, v); err != nil {
		return err
	}
	cfg.TLS = t
	return nil
}

func verifyChain(t *tls.Config, certs []*x509.Certificate) error {
	if len(certs) == 0 {
		return errors.New("mysql: server sent no certificate")
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{Roots: t.RootCAs, Intermediates: inter})
	return err
}

type conn struct {
	sqlconn.Conn
	drv     driver
	mu      sync.Mutex
	multiDB *sql.DB
}

// multi lazily opens the pool that allows several statements per query.
func (c *conn) multi(cfg *mysqldrv.Config) (*sql.DB, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.multiDB != nil {
		return c.multiDB, nil
	}
	mc := cfg.Clone()
	mc.MultiStatements = true
	connector, err := mysqldrv.NewConnector(mc)
	if err != nil {
		return nil, err
	}
	c.multiDB = sql.OpenDB(connector)
	c.multiDB.SetMaxOpenConns(2)
	return c.multiDB, nil
}

func (c *conn) closeMulti() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.multiDB == nil {
		return nil
	}
	return c.multiDB.Close()
}

func (c *conn) Ping(ctx context.Context) (plugin.ServerInfo, error) {
	var version string
	if err := c.DB.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return plugin.ServerInfo{}, connectError(err)
	}
	if strings.Contains(version, "MariaDB") {
		return plugin.ServerInfo{Version: "MariaDB " + strings.SplitN(version, "-MariaDB", 2)[0]}, nil
	}
	return plugin.ServerInfo{Version: "MySQL " + version}, nil
}

var (
	grantPrivileges = regexp.MustCompile(`(?i)^GRANT (.+?) ON `)
	writePrivilege  = regexp.MustCompile(`(?i)\b(ALL PRIVILEGES|INSERT|UPDATE|DELETE|CREATE|DROP|ALTER|INDEX|TRIGGER|EVENT|CREATE ROUTINE|ALTER ROUTINE|SUPER)\b`)
)

// CanWrite reads SHOW GRANTS. Grants of roles (MySQL 8) are not expanded, so a user whose only
// grants are roles is reported as unknown.
func (c *conn) CanWrite(ctx context.Context) (*bool, error) {
	rows, err := c.DB.QueryContext(ctx, "SHOW GRANTS FOR CURRENT_USER()")
	if err != nil {
		return nil, connectError(err)
	}
	defer func() { _ = rows.Close() }()
	can, sawPrivileges := false, false
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, connectError(err)
		}
		m := grantPrivileges.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		sawPrivileges = true
		if writePrivilege.MatchString(m[1]) {
			can = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, connectError(err)
	}
	if !sawPrivileges {
		return nil, nil
	}
	return &can, nil
}

func (c *conn) Schema(ctx context.Context) (*plugin.DBSchema, error) {
	rows, err := c.DB.QueryContext(ctx, `
		SELECT t.TABLE_NAME, t.TABLE_TYPE, COALESCE(t.TABLE_COMMENT, ''), c.COLUMN_NAME, c.COLUMN_TYPE,
			c.DATA_TYPE, c.IS_NULLABLE, COALESCE(c.COLUMN_COMMENT, '')
		FROM information_schema.TABLES t
		JOIN information_schema.COLUMNS c ON c.TABLE_SCHEMA = t.TABLE_SCHEMA AND c.TABLE_NAME = t.TABLE_NAME
		WHERE t.TABLE_SCHEMA = DATABASE()
		ORDER BY t.TABLE_NAME, c.ORDINAL_POSITION`)
	if err != nil {
		return nil, connectError(err)
	}
	defer func() { _ = rows.Close() }()
	var tables []plugin.Table
	for rows.Next() {
		var name, kind, comment, col, colType, dataType, nullable, colComment string
		if err := rows.Scan(&name, &kind, &comment, &col, &colType, &dataType, &nullable, &colComment); err != nil {
			return nil, connectError(err)
		}
		if n := len(tables); n == 0 || tables[n-1].Name != name {
			k := "table"
			if strings.Contains(kind, "VIEW") {
				k = "view"
			}
			tables = append(tables, plugin.Table{Name: name, Kind: k, Comment: comment})
		}
		t := &tables[len(tables)-1]
		vt, _ := c.drv.typeOf(dataType, colType)
		t.Columns = append(t.Columns, plugin.TableColumn{Name: col, Type: vt, DBType: colType, Nullable: nullable == "YES", Comment: colComment})
	}
	return &plugin.DBSchema{Tables: tables}, rows.Err()
}

func connectError(err error) error {
	if ce, ok := plugin.AsConnError(err); ok {
		return ce
	}
	var me *mysqldrv.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 1045, 1698:
			return plugin.NewConnError(plugin.ErrCodeAuthFailed, err)
		case 1049, 1044:
			// 1044 (access denied to the database) is also what servers answer for a database that
			// does not exist when the user could not see it anyway.
			return plugin.NewConnError(plugin.ErrCodeDatabaseNotFound, err)
		case 3159:
			return plugin.NewConnError(plugin.ErrCodeTLSRequired, err)
		}
	}
	msg := err.Error()
	var netErr net.Error
	switch {
	case errors.Is(err, netx.ErrBlocked):
		return plugin.NewConnError(plugin.ErrCodeNetworkBlocked, err)
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return plugin.NewConnError(plugin.ErrCodeTimeout, err)
	case strings.Contains(msg, "x509:") || strings.Contains(msg, "tls:") || strings.Contains(msg, "TLS requested but server does not support TLS"):
		return plugin.NewConnError(plugin.ErrCodeTLSFailed, err)
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"), strings.Contains(msg, "dial"):
		return plugin.NewConnError(plugin.ErrCodeHostUnreachable, err)
	}
	return plugin.NewConnError(plugin.ErrCodeFailed, err)
}

type driver struct {
	mariadb bool
}

func (*driver) Dialect() sqlscan.Dialect { return sqlscan.MySQL }

func (*driver) TxOptions(opts plugin.QueryOptions) *sql.TxOptions {
	return &sql.TxOptions{ReadOnly: opts.ReadOnly}
}

func (d *driver) SessionStatements(opts plugin.QueryOptions) []string {
	if opts.Timeout <= 0 {
		return nil
	}
	if d.mariadb {
		return []string{fmt.Sprintf("SET SESSION max_statement_time = %.3f", opts.Timeout.Seconds())}
	}
	return []string{fmt.Sprintf("SET SESSION max_execution_time = %d", opts.Timeout.Milliseconds())}
}

// ServerCancel kills the running statement from another connection when ctx ends early; the
// driver alone would only close the socket and leave the statement running.
func (*driver) ServerCancel(ctx context.Context, db *sql.DB, conn *sql.Conn) (func(), error) {
	var id int64
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-ctx.Done():
			// ctx is already done; the kill needs its own deadline.
			kctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, _ = db.ExecContext(kctx, fmt.Sprintf("KILL QUERY %d", id))
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }, nil
}

// typeOf maps DATA_TYPE (schema) or the driver's type name (results); the second result marks
// types with a time zone. TIMESTAMP is an instant, converted to UTC for the session.
func (d *driver) typeOf(name, full string) (plugin.ValueType, bool) {
	n := strings.ToUpper(strings.TrimPrefix(strings.ToUpper(name), "UNSIGNED "))
	switch n {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT", "YEAR":
		return plugin.TypeInt, false
	case "DECIMAL", "NUMERIC":
		return plugin.TypeDecimal, false
	case "FLOAT", "DOUBLE", "REAL":
		return plugin.TypeFloat, false
	case "BOOL", "BOOLEAN":
		return plugin.TypeBool, false
	case "DATE":
		return plugin.TypeDate, false
	case "DATETIME":
		return plugin.TypeDateTime, false
	case "TIMESTAMP":
		return plugin.TypeDateTime, true
	case "TIME":
		return plugin.TypeTime, false
	case "JSON":
		return plugin.TypeJSON, false
	case "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BINARY", "VARBINARY", "BIT", "GEOMETRY":
		return plugin.TypeBinary, false
	case "CHAR", "VARCHAR", "TEXT", "TINYTEXT", "MEDIUMTEXT", "LONGTEXT", "ENUM", "SET":
		// MariaDB stores JSON as LONGTEXT with a json_valid check.
		if strings.Contains(strings.ToLower(full), "json") {
			return plugin.TypeJSON, false
		}
		return plugin.TypeText, false
	}
	return plugin.TypeUnknown, false
}

func (d *driver) Column(ct *sql.ColumnType) plugin.Column {
	name := ct.DatabaseTypeName()
	t, tz := d.typeOf(name, "")
	if t == plugin.TypeDecimal {
		if _, scale, ok := ct.DecimalSize(); ok && scale == 0 && strings.HasSuffix(strings.ToUpper(name), "INT") {
			t = plugin.TypeInt
		}
	}
	return plugin.Column{Name: ct.Name(), Type: t, DBType: strings.ToLower(name), WithTimeZone: tz}
}

// Convert handles both protocols: text results arrive as []byte, binary (prepared) ones as Go
// numbers.
func (*driver) Convert(col plugin.Column, v any) (any, error) {
	s, isBytes := v.([]byte)
	switch col.Type {
	case plugin.TypeInt:
		switch x := v.(type) {
		case int64:
			return x, nil
		case uint64:
			return int64(x), nil //nolint:gosec // values above MaxInt64 only occur for BIGINT UNSIGNED
		}
		if isBytes {
			return strconv.ParseInt(string(s), 10, 64)
		}
	case plugin.TypeDecimal:
		if isBytes {
			return plugin.Decimal(s), nil
		}
		return plugin.Decimal(fmt.Sprint(v)), nil
	case plugin.TypeFloat:
		switch x := v.(type) {
		case float64:
			return x, nil
		case float32:
			return float64(x), nil
		}
		if isBytes {
			return strconv.ParseFloat(string(s), 64)
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
	case plugin.TypeDateTime:
		if t, ok := v.(time.Time); ok {
			return t.UTC(), nil
		}
	case plugin.TypeTime:
		return plugin.TimeOfDay(asString(v)), nil
	case plugin.TypeJSON:
		return plugin.JSON(asString(v)), nil
	case plugin.TypeBinary:
		if isBytes {
			return append([]byte(nil), s...), nil
		}
	}
	return asString(v), nil
}

func asString(v any) string {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(v)
}

func (*driver) QueryError(err error) *plugin.ConnError {
	var me *mysqldrv.MySQLError
	if !errors.As(err, &me) {
		return nil
	}
	switch me.Number {
	case 3024, 1969:
		return plugin.NewConnError(plugin.ErrCodeQueryTimeout, err)
	case 1317:
		return plugin.NewConnError(plugin.ErrCodeQueryCancelled, err)
	case 1792:
		return plugin.NewConnError(plugin.ErrCodeReadOnlyViolation, err)
	}
	return nil
}
