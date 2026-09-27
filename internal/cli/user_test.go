package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/security"
	"github.com/rowbird/rowbird/internal/store"
)

// cliEnv is a data directory shared by CLI invocations and a service opened on the same store.
type cliEnv struct {
	t       *testing.T
	environ []string
	dataDir string
}

func newCLIEnv(t *testing.T) *cliEnv {
	dir := t.TempDir()
	return &cliEnv{t: t, dataDir: dir, environ: []string{"ROWBIRD_DATA_DIR=" + dir}}
}

func (e *cliEnv) run(stdin string, args ...string) result {
	e.t.Helper()
	var out, errOut bytes.Buffer
	code := Execute(e.t.Context(), args, Env{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut,
		Environ: func() []string { return e.environ },
	})
	return result{code, out.String(), errOut.String()}
}

// service opens the same store as the CLI, for arranging and asserting.
func (e *cliEnv) service() (*auth.Service, *store.Store) {
	e.t.Helper()
	mk, err := crypto.LoadMasterKey(crypto.MasterKeySource{DataDir: e.dataDir})
	if err != nil {
		e.t.Fatal(err)
	}
	kr, _ := crypto.NewKeyring(mk.Raw)
	st, err := store.Open(e.t.Context(), security.Secret("sqlite://"+filepath.ToSlash(filepath.Join(e.dataDir, "rowbird.db"))), nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(e.t.Context()); err != nil {
		e.t.Fatal(err)
	}
	return auth.NewService(st, kr, auth.Config{}, auth.Options{
		Hasher: auth.NewPasswordHasher(auth.Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, 2),
	}), st
}

var tempPassword = regexp.MustCompile(`temporary password(?: for \S+)?: (\S+)`)

func login(t *testing.T, svc *auth.Service, email, password string) *auth.IssuedSession {
	t.Helper()
	res, err := svc.Login(context.Background(), email, password, auth.RequestMeta{})
	if err != nil || res.Session == nil {
		t.Fatalf("login %s: %v", email, err)
	}
	return res.Session
}

func TestUserCommands(t *testing.T) {
	e := newCLIEnv(t)

	if r := e.run("", "user", "create", "--email", "a@example.com", "--name", "A"); r.code != 1 || !strings.Contains(r.stderr, "setup has not been completed") {
		t.Fatalf("before setup: %+v", r)
	}

	svc, st := e.service()
	if _, err := svc.Setup(t.Context(), auth.SetupInput{Email: "admin@example.com", Name: "Admin", Password: "admin passphrase 1", Locale: "en", Timezone: "UTC"}, auth.RequestMeta{}); err != nil {
		t.Fatal(err)
	}

	r := e.run("", "user", "create", "--email", "Ed@Example.com", "--name", "Ed", "--role", "editor")
	m := tempPassword.FindStringSubmatch(r.stdout)
	if r.code != 0 || m == nil {
		t.Fatalf("create: %+v", r)
	}
	if s := login(t, svc, "ed@example.com", m[1]); s.Principal.Role != store.RoleEditor || s.Principal.Restriction != auth.RestrictionPasswordChange {
		t.Fatalf("created user session %+v", s.Principal)
	}

	r = e.run("robot passphrase 9\n", "user", "create", "--email", "bot@example.com", "--name", "Bot", "--role", "admin", "--password-stdin")
	if r.code != 0 || strings.Contains(r.stdout, "robot passphrase") || strings.Contains(r.stdout, "temporary") {
		t.Fatalf("create with stdin password: %+v", r)
	}
	if s := login(t, svc, "bot@example.com", "robot passphrase 9"); s.Principal.Restriction != auth.RestrictionNone {
		t.Fatal("a chosen password must not be temporary")
	}

	if r := e.run("", "user", "create", "--email", "ed@example.com", "--name", "Dup"); r.code != 1 || !strings.Contains(r.stderr, "user.email_taken") {
		t.Fatalf("duplicate: %+v", r)
	}
	if r := e.run("short\n", "user", "create", "--email", "x@example.com", "--name", "X", "--password-stdin"); r.code != 1 || !strings.Contains(r.stderr, "validation.too_short") {
		t.Fatalf("weak password: %+v", r)
	}

	if r := e.run("", "user", "set-role", "--email", "ed@example.com", "--role", "admin"); r.code != 0 {
		t.Fatalf("set-role: %+v", r)
	}
	for _, email := range []string{"ed@example.com", "bot@example.com"} {
		if r := e.run("", "user", "set-role", "--email", email, "--role", "viewer"); r.code != 0 {
			t.Fatalf("demote %s: %+v", email, r)
		}
	}
	if r := e.run("", "user", "set-role", "--email", "admin@example.com", "--role", "viewer"); r.code != 1 || !strings.Contains(r.stderr, "user.last_admin") {
		t.Fatalf("demote last admin: %+v", r)
	}
	if r := e.run("", "user", "set-role", "--email", "nobody@example.com", "--role", "viewer"); r.code != 1 || !strings.Contains(r.stderr, "no user") {
		t.Fatalf("unknown user: %+v", r)
	}

	r = e.run("", "user", "reset-password", "--email", "admin@example.com")
	m = tempPassword.FindStringSubmatch(r.stdout)
	if r.code != 0 || m == nil {
		t.Fatalf("reset: %+v", r)
	}
	if s := login(t, svc, "admin@example.com", m[1]); s.Principal.Restriction != auth.RestrictionPasswordChange {
		t.Fatal("reset password is not temporary")
	}

	if r := e.run("", "user", "disable-2fa", "--email", "admin@example.com"); r.code != 1 || !strings.Contains(r.stderr, "totp.not_enabled") {
		t.Fatalf("disable-2fa without 2fa: %+v", r)
	}

	ctx := store.WithWorkspace(t.Context(), mustWorkspace(t, st))
	page, _ := st.SecurityEvents().List(ctx, store.SecurityEventFilter{Type: auth.EventPasswordReset}, store.PageRequest{})
	if len(page.Items) != 1 || page.Items[0].ActorUserID != nil {
		t.Fatalf("CLI events must have no actor: %+v", page.Items)
	}
}

func mustWorkspace(t *testing.T, st *store.Store) [16]byte {
	t.Helper()
	ws, err := st.Workspaces().GetBySlug(t.Context(), auth.DefaultWorkspaceSlug)
	if err != nil {
		t.Fatal(err)
	}
	return ws.ID
}
