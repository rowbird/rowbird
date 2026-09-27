//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rowbird/rowbird/internal/connector/sshtunnel/sshtest"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/connectortest"
)

func startPostgres(t *testing.T) (host string, port int, adminURL string) {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("app"), tcpostgres.WithUsername("admin"), tcpostgres.WithPassword("admin-pw"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	connectortest.Must(t, err, "start postgres")
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	adminURL, err = c.ConnectionString(ctx, "sslmode=disable")
	connectortest.Must(t, err, "connection string")
	u, _ := url.Parse(adminURL)
	h, p, _ := net.SplitHostPort(u.Host)
	port, _ = strconv.Atoi(p)
	return h, port, adminURL
}

func TestConformance(t *testing.T) {
	host, port, adminURL := startPostgres(t)
	base := func(user, password string) map[string]any {
		return map[string]any{"host": host, "port": int64(port), "database": "app", "user": user, "password": password, "tls_mode": "disable", "connect_timeout": int64(10)}
	}
	withDB := func(v map[string]any, db string) map[string]any { v["database"] = db; return v }
	closedPort := func() int64 {
		ln, _ := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
		p := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		return int64(p)
	}()
	unreachable := base("admin", "admin-pw")
	unreachable["host"], unreachable["port"] = "127.0.0.1", closedPort

	connectortest.Run(t, connectortest.Harness{
		Connector:      connector{},
		Values:         base("admin", "admin-pw"),
		ReadOnlyValues: base("reader", "reader-pw"),
		Setup: func(t *testing.T) {
			db, err := sql.Open("pgx", adminURL)
			connectortest.Must(t, err, "open admin")
			defer func() { _ = db.Close() }()
			_, err = db.Exec(`
				CREATE TABLE rb_types (id int, c_int integer, c_bigint bigint, c_decimal numeric(38,9),
					c_float double precision, c_text text, c_bool boolean, c_date date, c_ts timestamp,
					c_tstz timestamptz, c_time time, c_json jsonb, c_bytea bytea, c_uuid uuid);
				INSERT INTO rb_types VALUES (1, 42, 9007199254740993, 12345678901234567890123456789.123456789,
					1.5, 'olá', true, '2026-09-25', '2026-09-25 10:11:12', '2026-09-25 13:11:12+03',
					'10:11:12', '{"a": 1}', '\x00ff', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11');
				INSERT INTO rb_types (id) VALUES (2);
				CREATE TABLE rb_rows (n int);
				INSERT INTO rb_rows SELECT generate_series(1, 10);
				CREATE ROLE reader LOGIN PASSWORD 'reader-pw';
				GRANT USAGE ON SCHEMA public TO reader;
				GRANT SELECT ON ALL TABLES IN SCHEMA public TO reader;`)
			connectortest.Must(t, err, "fixture")
		},
		Types: []connectortest.ExpectedColumn{
			{Name: "c_int", Type: plugin.TypeInt, Value: int64(42)},
			{Name: "c_bigint", Type: plugin.TypeInt, Value: int64(9007199254740993)},
			{Name: "c_decimal", Type: plugin.TypeDecimal, Value: plugin.Decimal("12345678901234567890123456789.123456789")},
			{Name: "c_float", Type: plugin.TypeFloat, Value: 1.5},
			{Name: "c_text", Type: plugin.TypeText, Value: "olá"},
			{Name: "c_bool", Type: plugin.TypeBool, Value: true},
			{Name: "c_date", Type: plugin.TypeDate, Value: plugin.Date("2026-09-25")},
			{Name: "c_ts", Type: plugin.TypeDateTime, Value: time.Date(2026, 9, 25, 10, 11, 12, 0, time.UTC)},
			{Name: "c_tstz", Type: plugin.TypeDateTime, WithTimeZone: true, Value: time.Date(2026, 9, 25, 10, 11, 12, 0, time.UTC)},
			{Name: "c_time", Type: plugin.TypeTime, Value: plugin.TimeOfDay("10:11:12")},
			{Name: "c_json", Type: plugin.TypeJSON, Value: plugin.JSON(`{"a": 1}`)},
			{Name: "c_bytea", Type: plugin.TypeBinary, Value: []byte{0, 255}},
			{Name: "c_uuid", Type: plugin.TypeText, Value: "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"},
		},
		SlowSQL:   "SELECT pg_sleep(30)",
		ParamSQL:  "SELECT $1::text",
		InsertSQL: "INSERT INTO rb_rows (n) VALUES (99)",
		Errors: map[string]map[string]any{
			plugin.ErrCodeAuthFailed:       base("admin", "wrong"),
			plugin.ErrCodeDatabaseNotFound: withDB(base("admin", "admin-pw"), "nope"),
			plugin.ErrCodeHostUnreachable:  unreachable,
		},
	})
}

// TestThroughSSHTunnel reaches the database via an in-process SSH server, and checks that the
// network policy applies to the SSH host.
func TestThroughSSHTunnel(t *testing.T) {
	host, port, _ := startPostgres(t)
	srv := sshtest.Start(t, "bastion", "ssh-pw", nil)
	values := map[string]any{
		"host": host, "port": int64(port), "database": "app", "user": "admin", "password": "admin-pw", "tls_mode": "disable",
		"ssh_enabled": true, "ssh_host": "127.0.0.1", "ssh_port": int64(srv.Port), "ssh_user": "bastion",
		"ssh_auth": "password", "ssh_password": "ssh-pw",
	}
	open := func(policy string) (plugin.Conn, error) {
		return connector{}.Open(t.Context(), plugin.OpenOptions{Values: values, Dial: netx.NewDialer(policy).DialContext})
	}

	_, err := open(netx.PolicyOpen)
	ce, ok := plugin.AsConnError(err)
	if !ok || ce.Code != plugin.ErrCodeSSHHostKeyUnknown || ce.Detail["fingerprint"] != srv.Fingerprint {
		t.Fatalf("first contact: %v", err)
	}
	values["ssh_host_key"] = srv.Fingerprint
	c, err := open(netx.PolicyOpen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if len(srv.Forwarded()) == 0 {
		t.Fatal("the connection did not go through the tunnel")
	}
	if _, err := open(netx.PolicyBlockPrivate); err == nil {
		t.Fatal("block-private allowed a loopback SSH host")
	}
}
