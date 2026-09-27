//go:build integration

package mssql

import (
	"context"
	"database/sql"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	mssqldb "github.com/microsoft/go-mssqldb"
	"github.com/testcontainers/testcontainers-go"
	tcmssql "github.com/testcontainers/testcontainers-go/modules/mssql"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/connectortest"
)

const saPassword = "Rowbird-sa-Pw1!"

func TestConformance(t *testing.T) {
	if os.Getenv("ROWBIRD_TEST_MSSQL") == "" {
		t.Skip("set ROWBIRD_TEST_MSSQL=1 to run (amd64-only image, about 1.6 GB)")
	}
	ctx := context.Background()
	// The image only exists for amd64; on arm64 hosts Docker runs it emulated.
	c, err := tcmssql.Run(ctx, "mcr.microsoft.com/mssql/server:2022-latest", tcmssql.WithAcceptEULA(), tcmssql.WithPassword(saPassword),
		testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{ImagePlatform: "linux/amd64"}}))
	connectortest.Must(t, err, "start sql server")
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	saURL, err := c.ConnectionString(ctx)
	connectortest.Must(t, err, "connection string")
	endpoint, err := c.PortEndpoint(ctx, "1433/tcp", "")
	connectortest.Must(t, err, "endpoint")
	host, portStr, _ := net.SplitHostPort(endpoint)
	port, _ := strconv.Atoi(portStr)

	base := func(user, password string) map[string]any {
		return map[string]any{"host": host, "port": int64(port), "database": "app", "user": user, "password": password, "tls_mode": "require", "connect_timeout": int64(15)}
	}
	wrongDB := base("app_admin", "Admin-pw-123!")
	wrongDB["database"] = "nope"
	unreachable := base("app_admin", "Admin-pw-123!")
	unreachable["host"], unreachable["port"] = "127.0.0.1", int64(1)

	connectortest.Run(t, connectortest.Harness{
		Connector:      connector{},
		Values:         base("app_admin", "Admin-pw-123!"),
		ReadOnlyValues: base("app_reader", "Reader-pw-123!"),
		Setup: func(t *testing.T) {
			connector, err := mssqldb.NewConnector(saURL + "&TrustServerCertificate=true")
			connectortest.Must(t, err, "sa connector")
			db := sql.OpenDB(connector)
			defer func() { _ = db.Close() }()
			for _, stmt := range []string{
				"CREATE DATABASE app",
				"CREATE LOGIN app_admin WITH PASSWORD = 'Admin-pw-123!', CHECK_POLICY = OFF",
				"CREATE LOGIN app_reader WITH PASSWORD = 'Reader-pw-123!', CHECK_POLICY = OFF",
				`USE app;
				CREATE USER app_admin FOR LOGIN app_admin; ALTER ROLE db_owner ADD MEMBER app_admin;
				CREATE USER app_reader FOR LOGIN app_reader; ALTER ROLE db_datareader ADD MEMBER app_reader;
				CREATE TABLE rb_types (id int, c_int int, c_bigint bigint, c_decimal decimal(38,9), c_float float,
					c_text nvarchar(20), c_bool bit, c_date date, c_dt datetime2, c_dto datetimeoffset, c_time time,
					c_bin varbinary(4), c_uuid uniqueidentifier);
				INSERT INTO rb_types VALUES (1, 42, 9007199254740993, 12345678901234567890123456789.123456789, 1.5,
					N'olá', 1, '2026-09-25', '2026-09-25 10:11:12', '2026-09-25 13:11:12 +03:00', '10:11:12',
					0x00FF, 'A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11');
				INSERT INTO rb_types (id) VALUES (2);
				CREATE TABLE rb_rows (n int);
				INSERT INTO rb_rows VALUES (1),(2),(3),(4),(5),(6),(7),(8),(9),(10);`,
			} {
				_, err := db.ExecContext(ctx, stmt)
				connectortest.Must(t, err, stmt[:min(len(stmt), 20)])
			}
		},
		Types: []connectortest.ExpectedColumn{
			{Name: "c_int", Type: plugin.TypeInt, Value: int64(42)},
			{Name: "c_bigint", Type: plugin.TypeInt, Value: int64(9007199254740993)},
			{Name: "c_decimal", Type: plugin.TypeDecimal, Value: plugin.Decimal("12345678901234567890123456789.123456789")},
			{Name: "c_float", Type: plugin.TypeFloat, Value: 1.5},
			{Name: "c_text", Type: plugin.TypeText, Value: "olá"},
			{Name: "c_bool", Type: plugin.TypeBool, Value: true},
			{Name: "c_date", Type: plugin.TypeDate, Value: plugin.Date("2026-09-25")},
			{Name: "c_dt", Type: plugin.TypeDateTime, Value: time.Date(2026, 9, 25, 10, 11, 12, 0, time.UTC)},
			{Name: "c_dto", Type: plugin.TypeDateTime, WithTimeZone: true, Value: time.Date(2026, 9, 25, 10, 11, 12, 0, time.UTC)},
			{Name: "c_time", Type: plugin.TypeTime, Value: plugin.TimeOfDay("10:11:12")},
			{Name: "c_bin", Type: plugin.TypeBinary, Value: []byte{0, 255}},
			{Name: "c_uuid", Type: plugin.TypeText, Value: "A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11"},
		},
		// One statement, since two are refused: a cross join that counts billions of rows.
		SlowSQL:   "SELECT COUNT_BIG(*) FROM sys.all_columns a CROSS JOIN sys.all_columns b CROSS JOIN sys.all_columns c",
		ParamSQL:  "SELECT CAST(@p1 AS nvarchar(100))",
		InsertSQL: "INSERT INTO rb_rows (n) VALUES (99)",
		Errors: map[string]map[string]any{
			plugin.ErrCodeAuthFailed:       base("app_admin", "wrong"),
			plugin.ErrCodeDatabaseNotFound: wrongDB,
			plugin.ErrCodeHostUnreachable:  unreachable,
		},
	})
}
