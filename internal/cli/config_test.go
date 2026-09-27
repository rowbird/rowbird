package cli

import (
	"database/sql"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/app"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/config"
	"github.com/rowbird/rowbird/internal/logging"
	"github.com/rowbird/rowbird/internal/store"
)

// withWorkspace completes setup and creates the SQLite database the documents use.
func (e *cliEnv) withWorkspace() *auth.IssuedSession {
	e.t.Helper()
	svc, _ := e.service()
	s, err := svc.Setup(e.t.Context(), auth.SetupInput{Email: "admin@example.com", Name: "Admin", Password: "admin passphrase 1", Locale: "en", Timezone: "UTC"}, auth.RequestMeta{})
	if err != nil {
		e.t.Fatal(err)
	}
	dir := filepath.Join(e.dataDir, "sqlite")
	_ = os.MkdirAll(dir, 0o700)
	db, _ := sql.Open("sqlite", "file:"+filepath.Join(dir, "shop.db"))
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE orders (id INTEGER, total DECIMAL(10,2))"); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *cliEnv) documents(extra string) string {
	e.t.Helper()
	path := filepath.Join(e.t.TempDir(), "rowbird")
	_ = os.MkdirAll(path, 0o700)
	conn := "apiVersion: rowbird.dev/v1\nkind: Connection\nmetadata: {name: shop}\nspec:\n  driver: sqlite\n  config: {path: " + filepath.Join(e.dataDir, "sqlite", "shop.db") + "}\n"
	report := `apiVersion: rowbird.dev/v1
kind: Report
metadata: {name: big-orders, title: Big orders}
spec:
  query:
    connection: shop
    sql: select * from orders where total > {{min}}
    params: [{name: min, type: decimal, default: "100"}]
  schedule: {cron: "0 8 * * 1-5", timezone: UTC}
` + extra
	_ = os.WriteFile(filepath.Join(path, "10-connection.yaml"), []byte(conn), 0o600)
	_ = os.WriteFile(filepath.Join(path, "20-report.yaml"), []byte(report), 0o600)
	return path
}

func TestApplyAndExportLocally(t *testing.T) {
	e := newCLIEnv(t)
	e.withWorkspace()
	dir := e.documents("")

	r := e.run("", "apply", "-f", dir, "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "+ create    Connection/shop") || !strings.Contains(r.stdout, "+ create    Report/big-orders") ||
		!strings.Contains(r.stdout, "Plan (dry run, nothing changed): 3 to create") {
		t.Fatalf("dry run %+v", r)
	}
	if r := e.run("", "export"); strings.Contains(r.stdout, "big-orders") {
		t.Fatalf("the dry run saved something:\n%s", r.stdout)
	}
	if r := e.run("", "apply", "-f", dir); r.code != 0 || !strings.Contains(r.stdout, "Applied: 3 to create") {
		t.Fatalf("apply %+v", r)
	}
	if r := e.run("", "apply", "-f", dir); r.code != 0 || !strings.Contains(r.stdout, "0 to create, 0 to update, 3 unchanged") {
		t.Fatalf("second apply %+v", r)
	}

	out := filepath.Join(t.TempDir(), "export.yaml")
	if r := e.run("", "export", "-o", out, "--report", "big-orders", "--with-connections"); r.code != 0 {
		t.Fatalf("export %+v", r)
	}
	data, _ := os.ReadFile(out)
	for _, want := range []string{"kind: Connection", "kind: Query", "name: big-orders", "ref: big-orders"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("export lacks %q:\n%s", want, data)
		}
	}

	// A conflict fails by default and names the choices.
	changed := e.documents("  enabled: false\n")
	r = e.run("", "apply", "-f", changed)
	if r.code != 1 || !strings.Contains(r.stdout, "! conflict  Report/big-orders") || !strings.Contains(r.stdout, "spec.enabled: true -> false") {
		t.Fatalf("conflict %+v", r)
	}
	if r := e.run("", "apply", "-f", changed, "--policy", "overwrite"); r.code != 0 || !strings.Contains(r.stdout, "~ update    Report/big-orders") {
		t.Fatalf("overwrite %+v", r)
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(bad, []byte("apiVersion: rowbird.dev/v1\nkind: Query\nmetadata: {name: q}\nspec: {connection: shop, sqll: x}\n"), 0o600)
	if r := e.run("", "apply", "-f", bad); r.code != 1 || !strings.Contains(r.stdout, "bad.yaml:4:") {
		t.Fatalf("bad file %+v", r)
	}
}

func TestApplyThroughTheServer(t *testing.T) {
	e := newCLIEnv(t)
	session := e.withWorkspace()
	cfg, err := config.Load(config.Options{Environ: func() []string { return e.environ }})
	if err != nil {
		t.Fatal(err)
	}
	logger, _ := logging.New(&strings.Builder{}, "error", "text")
	a, err := app.New(t.Context(), cfg, logger, app.Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	svc, _ := e.service()
	p, err := svc.AuthenticateSession(t.Context(), session.Token)
	if err != nil {
		t.Fatal(err)
	}
	key, err := svc.CreateAPIKey(store.WithWorkspace(t.Context(), p.WorkspaceID), p, auth.NewAPIKeyInput{Name: "ci", Scopes: []auth.Scope{auth.ScopeRead, auth.ScopeAdmin}}, auth.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	dir := e.documents("")
	// The CLI's own environment holds the key; no local store is touched.
	e.environ = []string{"ROWBIRD_API_KEY=" + key.Plaintext, "ROWBIRD_DATA_DIR=" + t.TempDir()}
	if r := e.run("", "apply", "-f", dir, "--server", srv.URL); r.code != 0 || !strings.Contains(r.stdout, "Applied: 3 to create") {
		t.Fatalf("remote apply %+v", r)
	}
	r := e.run("", "export", "--server", srv.URL, "--report", "big-orders")
	if r.code != 0 || !strings.Contains(r.stdout, "name: big-orders") {
		t.Fatalf("remote export %+v", r)
	}
	// Positions in the server's plan point at the files.
	changed := e.documents("  enabled: false\n")
	r = e.run("", "apply", "-f", changed, "--server", srv.URL)
	if r.code != 1 || !strings.Contains(r.stdout, "! conflict  Report/big-orders") {
		t.Fatalf("remote conflict %+v", r)
	}
	missing := e.documents("  deliveries: [{channel: nowhere, mode: inline}]\n")
	r = e.run("", "apply", "-f", missing, "--server", srv.URL)
	if r.code != 1 || !strings.Contains(r.stdout, `20-report.yaml:1:1 (use --map nowhere=<existing>)`) {
		t.Fatalf("remote missing %+v", r)
	}
}
