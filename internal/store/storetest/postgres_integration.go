//go:build integration

package storetest

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rowbird/rowbird/internal/store/ids"
)

// One container per test binary; each test gets its own database inside it. The container is
// removed by the testcontainers reaper when the test process exits.
var (
	pgOnce    sync.Once
	pgBaseURL string
	pgErr     error
)

func init() {
	postgresURL = newPostgresDatabase
}

func newPostgresDatabase(t *testing.T) string {
	t.Helper()
	pgOnce.Do(func() {
		ctx := context.Background()
		c, err := postgres.Run(ctx, "postgres:17-alpine",
			postgres.WithDatabase("rowbird"),
			postgres.WithUsername("rowbird"),
			postgres.WithPassword("rowbird"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
		)
		if err != nil {
			pgErr = fmt.Errorf("start postgres container: %w", err)
			return
		}
		pgBaseURL, pgErr = c.ConnectionString(ctx, "sslmode=disable")
	})
	if pgErr != nil {
		t.Fatal(pgErr)
	}

	name := "t_" + strings.ReplaceAll(ids.New().String(), "-", "")
	admin, err := sql.Open("pgx", pgBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	u, err := url.Parse(pgBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}
