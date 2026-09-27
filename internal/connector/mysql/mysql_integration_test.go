//go:build integration

package mysql

import (
	"context"
	"database/sql"
	"net"
	"strconv"
	"testing"
	"time"

	mysqldrv "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"
	tcmariadb "github.com/testcontainers/testcontainers-go/modules/mariadb"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/connectortest"
)

type flavor struct {
	name  string
	start func(ctx context.Context) (testcontainers.Container, error)
	// jsonColumn differs: MariaDB's JSON is LONGTEXT with a check constraint.
	jsonType plugin.ValueType
	// slowSQL: MySQL's max_execution_time interrupts SLEEP() without an error (it returns 1), so
	// a real, expensive query is used there, as a user's query would be.
	slowSQL string
}

var flavors = []flavor{
	{"mysql", func(ctx context.Context) (testcontainers.Container, error) {
		return tcmysql.Run(ctx, "mysql:8.4", tcmysql.WithDatabase("app"), tcmysql.WithUsername("admin"), tcmysql.WithPassword("admin-pw"))
	}, plugin.TypeJSON, "SELECT COUNT(*) FROM information_schema.COLUMNS a, information_schema.COLUMNS b, information_schema.COLUMNS c"},
	{"mariadb", func(ctx context.Context) (testcontainers.Container, error) {
		return tcmariadb.Run(ctx, "mariadb:11.4", tcmariadb.WithDatabase("app"), tcmariadb.WithUsername("admin"), tcmariadb.WithPassword("admin-pw"))
	}, plugin.TypeText, "SELECT SLEEP(30)"},
}

func TestConformance(t *testing.T) {
	for _, f := range flavors {
		t.Run(f.name, func(t *testing.T) {
			ctx := context.Background()
			c, err := f.start(ctx)
			connectortest.Must(t, err, "start ", f.name)
			t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
			endpoint, err := c.PortEndpoint(ctx, "3306/tcp", "")
			connectortest.Must(t, err, "endpoint")
			host, portStr, _ := net.SplitHostPort(endpoint)
			port, _ := strconv.Atoi(portStr)

			base := func(user, password string) map[string]any {
				return map[string]any{"host": host, "port": int64(port), "database": "app", "user": user, "password": password, "tls_mode": "disable", "connect_timeout": int64(10)}
			}
			rootCfg := mysqldrv.NewConfig()
			rootCfg.Net, rootCfg.Addr, rootCfg.User, rootCfg.Passwd, rootCfg.DBName = "tcp", net.JoinHostPort(host, strconv.Itoa(port)), "root", "admin-pw", "app"
			rootCfg.MultiStatements = true
			wrongDB := base("admin", "admin-pw")
			wrongDB["database"] = "nope"
			unreachable := base("admin", "admin-pw")
			unreachable["host"], unreachable["port"] = "127.0.0.1", int64(1)

			jsonValue := any(plugin.JSON(`{"a": 1}`))
			if f.jsonType == plugin.TypeText {
				jsonValue = `{"a": 1}`
			}

			connectortest.Run(t, connectortest.Harness{
				Connector:      connector{},
				Values:         base("admin", "admin-pw"),
				ReadOnlyValues: base("reader", "reader-pw"),
				Setup: func(t *testing.T) {
					db := sql.OpenDB(must(mysqldrv.NewConnector(rootCfg)))
					defer func() { _ = db.Close() }()
					var err error
					for range 30 { // root may lag behind the readiness log
						if err = db.PingContext(ctx); err == nil {
							break
						}
						time.Sleep(time.Second)
					}
					connectortest.Must(t, err, "ping root")
					_, err = db.Exec(`
						CREATE TABLE rb_types (id INT, c_int INT, c_bigint BIGINT, c_decimal DECIMAL(38,9),
							c_float DOUBLE, c_text VARCHAR(20), c_date DATE, c_datetime DATETIME,
							c_timestamp TIMESTAMP NULL, c_time TIME, c_json JSON, c_blob VARBINARY(4));
						INSERT INTO rb_types VALUES (1, 42, 9007199254740993, 12345678901234567890123456789.123456789,
							1.5, 'olá', '2026-09-25', '2026-09-25 10:11:12', '2026-09-25 10:11:12', '10:11:12',
							'{"a": 1}', 0x00ff);
						INSERT INTO rb_types (id) VALUES (2);
						CREATE TABLE rb_rows (n INT);
						INSERT INTO rb_rows VALUES (1),(2),(3),(4),(5),(6),(7),(8),(9),(10);
						CREATE USER 'reader'@'%' IDENTIFIED BY 'reader-pw';
						GRANT SELECT ON app.* TO 'reader'@'%';
						GRANT ALL PRIVILEGES ON app.* TO 'admin'@'%';`)
					connectortest.Must(t, err, "fixture")
				},
				Types: []connectortest.ExpectedColumn{
					{Name: "c_int", Type: plugin.TypeInt, Value: int64(42)},
					{Name: "c_bigint", Type: plugin.TypeInt, Value: int64(9007199254740993)},
					{Name: "c_decimal", Type: plugin.TypeDecimal, Value: plugin.Decimal("12345678901234567890123456789.123456789")},
					{Name: "c_float", Type: plugin.TypeFloat, Value: 1.5},
					{Name: "c_text", Type: plugin.TypeText, Value: "olá"},
					{Name: "c_date", Type: plugin.TypeDate, Value: plugin.Date("2026-09-25")},
					{Name: "c_datetime", Type: plugin.TypeDateTime, Value: time.Date(2026, 9, 25, 10, 11, 12, 0, time.UTC)},
					{Name: "c_timestamp", Type: plugin.TypeDateTime, WithTimeZone: true, Value: time.Date(2026, 9, 25, 10, 11, 12, 0, time.UTC)},
					{Name: "c_time", Type: plugin.TypeTime, Value: plugin.TimeOfDay("10:11:12")},
					{Name: "c_json", Type: f.jsonType, Value: jsonValue},
					{Name: "c_blob", Type: plugin.TypeBinary, Value: []byte{0, 255}},
				},
				SlowSQL:   f.slowSQL,
				ParamSQL:  "SELECT CAST(? AS CHAR)",
				InsertSQL: "INSERT INTO rb_rows (n) VALUES (99)",
				Errors: map[string]map[string]any{
					plugin.ErrCodeAuthFailed:       base("admin", "wrong"),
					plugin.ErrCodeDatabaseNotFound: wrongDB,
					plugin.ErrCodeHostUnreachable:  unreachable,
				},
			})
		})
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
