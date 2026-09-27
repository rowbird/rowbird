//go:build integration

package queries_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	"github.com/testcontainers/testcontainers-go"
	tcmariadb "github.com/testcontainers/testcontainers-go/modules/mariadb"
	tcmssql "github.com/testcontainers/testcontainers-go/modules/mssql"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/connections"
	_ "github.com/rowbird/rowbird/internal/connector/mssql"
	_ "github.com/rowbird/rowbird/internal/connector/mysql"
	_ "github.com/rowbird/rowbird/internal/connector/postgres"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/params"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

type target struct {
	name   string
	driver string
	start  func(ctx context.Context) (testcontainers.Container, error)
	port   string
	// admin opens the database with a superuser to create the fixture.
	adminDSN func(host string, port int) (driverName, dsn string)
	values   func(host string, port int) map[string]any
	fixture  []string
}

const fixtureRows = "INSERT INTO orders VALUES (1, 'south', 120.50, '2026-09-24 10:00:00'), (2, 'north', 80.00, '2026-09-25 09:00:00'), (3, 'south', 10.00, '2026-09-20 09:00:00'), (4, 'o''reilly', 1.00, '2026-09-25 09:00:00')"

var targets = []target{
	{
		name: "postgres", driver: "postgres", port: "5432/tcp",
		start: func(ctx context.Context) (testcontainers.Container, error) {
			return tcpostgres.Run(ctx, "postgres:17-alpine", tcpostgres.WithDatabase("app"), tcpostgres.WithUsername("admin"), tcpostgres.WithPassword("admin-pw"),
				testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)))
		},
		adminDSN: func(h string, p int) (string, string) {
			return "pgx", "postgres://admin:admin-pw@" + net.JoinHostPort(h, strconv.Itoa(p)) + "/app?sslmode=disable"
		},
		values: func(h string, p int) map[string]any {
			return map[string]any{"host": h, "port": int64(p), "database": "app", "user": "admin", "password": "admin-pw", "tls_mode": "disable"}
		},
		fixture: []string{"CREATE TABLE orders (id int, region text, total numeric(10,2), created_at timestamp)", fixtureRows},
	},
	{
		name: "mysql", driver: "mysql", port: "3306/tcp",
		start: func(ctx context.Context) (testcontainers.Container, error) {
			return tcmysql.Run(ctx, "mysql:8.4", tcmysql.WithDatabase("app"), tcmysql.WithUsername("admin"), tcmysql.WithPassword("admin-pw"))
		},
		adminDSN: func(h string, p int) (string, string) {
			return "mysql", "admin:admin-pw@tcp(" + net.JoinHostPort(h, strconv.Itoa(p)) + ")/app"
		},
		values: func(h string, p int) map[string]any {
			return map[string]any{"host": h, "port": int64(p), "database": "app", "user": "admin", "password": "admin-pw", "tls_mode": "disable"}
		},
		fixture: []string{"CREATE TABLE orders (id int, region varchar(20), total decimal(10,2), created_at datetime)", fixtureRows},
	},
	{
		name: "mariadb", driver: "mysql", port: "3306/tcp",
		start: func(ctx context.Context) (testcontainers.Container, error) {
			return tcmariadb.Run(ctx, "mariadb:11.4", tcmariadb.WithDatabase("app"), tcmariadb.WithUsername("admin"), tcmariadb.WithPassword("admin-pw"))
		},
		adminDSN: func(h string, p int) (string, string) {
			return "mysql", "admin:admin-pw@tcp(" + net.JoinHostPort(h, strconv.Itoa(p)) + ")/app"
		},
		values: func(h string, p int) map[string]any {
			return map[string]any{"host": h, "port": int64(p), "database": "app", "user": "admin", "password": "admin-pw", "tls_mode": "disable"}
		},
		fixture: []string{"CREATE TABLE orders (id int, region varchar(20), total decimal(10,2), created_at datetime)", fixtureRows},
	},
	{
		name: "mssql", driver: "mssql", port: "1433/tcp",
		start: func(ctx context.Context) (testcontainers.Container, error) {
			return tcmssql.Run(ctx, "mcr.microsoft.com/mssql/server:2022-latest", tcmssql.WithAcceptEULA(), tcmssql.WithPassword("Rowbird-sa-Pw1!"),
				testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{ImagePlatform: "linux/amd64"}}))
		},
		adminDSN: func(h string, p int) (string, string) {
			return "sqlserver", "sqlserver://sa:" + "Rowbird-sa-Pw1%21" + "@" + net.JoinHostPort(h, strconv.Itoa(p)) + "?database=master&TrustServerCertificate=true"
		},
		values: func(h string, p int) map[string]any {
			return map[string]any{"host": h, "port": int64(p), "database": "master", "user": "sa", "password": "Rowbird-sa-Pw1!", "tls_mode": "require"}
		},
		fixture: []string{"CREATE TABLE orders (id int, region nvarchar(20), total decimal(10,2), created_at datetime2)", fixtureRows},
	},
}

// TestPreviewAgainstDatabases runs the same parameterized preview on every driver: built-in dates,
// a decimal, and text with a quote, all bound as driver parameters.
func TestPreviewAgainstDatabases(t *testing.T) {
	for _, tg := range targets {
		t.Run(tg.name, func(t *testing.T) {
			if tg.name == "mssql" && os.Getenv("ROWBIRD_TEST_MSSQL") == "" {
				t.Skip("set ROWBIRD_TEST_MSSQL=1 to run (amd64-only image, about 1.6 GB)")
			}
			ctx := context.Background()
			c, err := tg.start(ctx)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
			endpoint, _ := c.PortEndpoint(ctx, tg.port, "")
			host, portStr, _ := net.SplitHostPort(endpoint)
			port, _ := strconv.Atoi(portStr)

			drv, dsn := tg.adminDSN(host, port)
			db, err := sql.Open(drv, dsn)
			if err != nil {
				t.Fatal(err)
			}
			for range 30 {
				if err = db.PingContext(ctx); err == nil {
					break
				}
				time.Sleep(time.Second)
			}
			for _, stmt := range tg.fixture {
				if _, err := db.ExecContext(ctx, stmt); err != nil {
					t.Fatalf("fixture: %v", err)
				}
			}
			_ = db.Close()

			st := storetest.Open(t, "sqlite://"+t.TempDir()+"/rb.db")
			if _, err := st.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			w := &store.Workspace{Name: "w", Slug: "w"}
			_ = st.Workspaces().Create(ctx, w)
			wctx := store.WithWorkspace(ctx, w.ID)
			key := make([]byte, crypto.KeySize)
			_, _ = rand.Read(key)
			kr, _ := crypto.NewKeyring(key)
			conns := connections.NewService(st, kr, connections.Options{Dial: netx.NewDialer(netx.PolicyOpen).DialContext})
			p := &auth.Principal{WorkspaceID: w.ID, Role: store.RoleAdmin}
			conn, err := conns.Create(wctx, p, connections.Input{Name: "db", Driver: tg.driver, Config: tg.values(host, port)}, auth.RequestMeta{})
			if err != nil || conn.Status != store.ConnectionOK {
				t.Fatalf("connection: %+v %v", conn, err)
			}
			now := func() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) }
			svc := queries.NewService(st, conns, queries.Options{Now: now})

			def := "50.00"
			res, err := svc.Preview(wctx, queries.PreviewInput{
				ConnectionID: conn.ID,
				SQL: "SELECT id, total FROM orders WHERE created_at >= {{yesterday}} AND created_at < {{today}} " +
					"AND region = {{region}} AND total >= {{min_total}} ORDER BY id",
				Params:   []params.Definition{{Name: "region", Type: params.Text}, {Name: "min_total", Type: params.Decimal, Default: &def}},
				Values:   map[string]string{"region": "south"},
				Timezone: "America/Sao_Paulo",
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Rows) != 1 || res.Rows[0][0] != int64(1) || res.Rows[0][1] != plugin.Decimal("120.50") {
				t.Fatalf("rows %v", res.Rows)
			}
			res, err = svc.Preview(wctx, queries.PreviewInput{
				ConnectionID: conn.ID,
				SQL:          "SELECT id FROM orders WHERE region = {{region}} AND region = {{region}}",
				Params:       []params.Definition{{Name: "region", Type: params.Text}},
				Values:       map[string]string{"region": "o'reilly"},
			})
			if err != nil || len(res.Rows) != 1 || res.Rows[0][0] != int64(4) {
				t.Fatalf("quoted, repeated parameter: %v %v", res, err)
			}
		})
	}
}
