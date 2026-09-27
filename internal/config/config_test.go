package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

func environ(kv ...string) func() []string {
	return func() []string { return kv }
}

func flags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return fs
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rowbird.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaults(t *testing.T) {
	cfg, err := Load(Options{Environ: environ()})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8080" || cfg.Workers != 4 || cfg.SchedulerTick != 5*time.Second || cfg.ShutdownTimeout != 30*time.Second || cfg.NoScheduler || cfg.NoWorkers {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.DataDir != "/data" || cfg.DatabaseURL.Reveal() != "sqlite:///data/rowbird.db" {
		t.Errorf("data dir %q, database url %q", cfg.DataDir, cfg.DatabaseURL.Reveal())
	}
	if cfg.LogLevel != "info" || cfg.LogFormat != "text" || !cfg.UpdateCheck || cfg.NetworkPolicy != "open" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}

func TestPrecedence(t *testing.T) {
	path := writeFile(t, `
server:
  listen_addr: ":7000"
  workers: 2
  log_level: debug
  log_format: json
other_section:
  listen_addr: ":1"
`)
	cases := []struct {
		name       string
		env        []string
		args       []string
		wantListen string
		wantWork   int
		wantLevel  string
	}{
		{"file over defaults", []string{"ROWBIRD_CONFIG_FILE=" + path}, nil, ":7000", 2, "debug"},
		{"env over file", []string{"ROWBIRD_CONFIG_FILE=" + path, "ROWBIRD_WORKERS=8"}, nil, ":7000", 8, "debug"},
		{"flag over env", []string{"ROWBIRD_WORKERS=8"}, []string{"--config", path, "--workers", "16", "--listen-addr", ":9000"}, ":9000", 16, "debug"},
		{"unset flags do not override", []string{"ROWBIRD_LISTEN_ADDR=:6000"}, []string{"--log-level", "warn"}, ":6000", 4, "warn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(Options{Environ: environ(tc.env...), Flags: flags(t, tc.args...)})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ListenAddr != tc.wantListen || cfg.Workers != tc.wantWork || cfg.LogLevel != tc.wantLevel {
				t.Errorf("got listen=%q workers=%d level=%q", cfg.ListenAddr, cfg.Workers, cfg.LogLevel)
			}
		})
	}
}

func TestEnvironmentParsing(t *testing.T) {
	cfg, err := Load(Options{Environ: environ(
		"ROWBIRD_SCHEDULER_TICK=10s",
		"ROWBIRD_UPDATE_CHECK=false",
		"ROWBIRD_TRUSTED_PROXIES=10.0.0.0/8, 192.168.0.0/16",
		"ROWBIRD_DATABASE_URL=postgres://u:p@db/rowbird",
		"ROWBIRD_BASE_URL=https://rowbird.example.com",
		"ROWBIRD_NETWORK_POLICY=block-private",
		"UNRELATED=1",
	)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchedulerTick != 10*time.Second || cfg.UpdateCheck {
		t.Errorf("tick=%s update=%v", cfg.SchedulerTick, cfg.UpdateCheck)
	}
	if strings.Join(cfg.TrustedProxies, "|") != "10.0.0.0/8|192.168.0.0/16" {
		t.Errorf("trusted proxies %v", cfg.TrustedProxies)
	}
	if cfg.DatabaseURL.Reveal() != "postgres://u:p@db/rowbird" {
		t.Errorf("database url %q", cfg.DatabaseURL.Reveal())
	}
	if len(cfg.Warnings()) != 0 {
		t.Errorf("unexpected warnings %v", cfg.Warnings())
	}
}

func TestDatabaseURLDerivedFromRelativeDataDir(t *testing.T) {
	cfg, err := Load(Options{Environ: environ("ROWBIRD_DATA_DIR=.data")})
	if err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	want := "sqlite://" + filepath.ToSlash(filepath.Join(wd, ".data", "rowbird.db"))
	if cfg.DatabaseURL.Reveal() != want {
		t.Errorf("got %q, want %q", cfg.DatabaseURL.Reveal(), want)
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]string{
		"ROWBIRD_WORKERS=0":                        "workers",
		"ROWBIRD_SCHEDULER_TICK=100ms":             "scheduler_tick",
		"ROWBIRD_SHUTDOWN_TIMEOUT=10ms":            "shutdown_timeout",
		"ROWBIRD_LOG_LEVEL=loud":                   "log level",
		"ROWBIRD_LOG_FORMAT=xml":                   "log_format",
		"ROWBIRD_NETWORK_POLICY=closed":            "network_policy",
		"ROWBIRD_STORAGE_BACKEND=ftp":              "storage_backend",
		"ROWBIRD_DATABASE_URL=mysql://x":           "database_url",
		"ROWBIRD_BASE_URL=rowbird.example.com":     "base_url",
		"ROWBIRD_LISTEN_ADDR=8080":                 "listen_addr",
		"ROWBIRD_TRUSTED_PROXIES=10.0.0.1":         "trusted_proxies",
		"ROWBIRD_CONFIG_FILE=/does/not/exist.yaml": "config file",
	}
	for kv, want := range cases {
		t.Run(kv, func(t *testing.T) {
			_, err := Load(Options{Environ: environ(kv)})
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error %v does not mention %q", err, want)
			}
		})
	}
}

func TestMasterKeySourcesAreExclusive(t *testing.T) {
	for _, env := range [][]string{
		{"ROWBIRD_MASTER_KEY=abc", "ROWBIRD_MASTER_KEY_FILE=/run/secrets/key"},
		{"ROWBIRD_MASTER_KEY_PREVIOUS=abc", "ROWBIRD_MASTER_KEY_PREVIOUS_FILE=/run/secrets/old"},
	} {
		_, err := Load(Options{Environ: environ(env...)})
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("%v: got %v", env, err)
		}
	}
	cfg, err := Load(Options{Environ: environ("ROWBIRD_MASTER_KEY_PREVIOUS_FILE=/run/secrets/old")})
	if err != nil || cfg.MasterKeyPreviousFile != "/run/secrets/old" {
		t.Fatalf("previous key file: %v", err)
	}
}

func TestErrorsDoNotLeakSecrets(t *testing.T) {
	_, err := Load(Options{Environ: environ(
		"ROWBIRD_DATABASE_URL=mysql://root:topsecretpw@h/db",
		"ROWBIRD_MASTER_KEY=topsecretkey",
		"ROWBIRD_MASTER_KEY_FILE=/k",
	)})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, s := range []string{"topsecretpw", "topsecretkey"} {
		if strings.Contains(err.Error(), s) {
			t.Errorf("error leaks %q: %v", s, err)
		}
	}
}

func TestProxiesCookiesAndSetupToken(t *testing.T) {
	cfg, err := Load(Options{Environ: environ(
		"ROWBIRD_TRUSTED_PROXIES=10.1.2.3/8,::1/128",
		"ROWBIRD_BASE_URL=https://rowbird.example.com",
		"ROWBIRD_SETUP_TOKEN=abc",
	)})
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.TrustedProxyPrefixes()
	if len(got) != 2 || got[0].String() != "10.0.0.0/8" || got[1].String() != "::1/128" {
		t.Fatalf("prefixes %v", got)
	}
	if !cfg.SecureCookies() || cfg.SetupToken.Reveal() != "abc" {
		t.Fatal("secure cookies or setup token")
	}
	plain, _ := Load(Options{Environ: environ("ROWBIRD_BASE_URL=http://localhost:8080")})
	if plain.SecureCookies() || !strings.Contains(strings.Join(plain.Warnings(), " "), "Secure") {
		t.Fatalf("http base URL: %v", plain.Warnings())
	}
}

func TestWarnings(t *testing.T) {
	cfg, err := Load(Options{Environ: environ()})
	if err != nil {
		t.Fatal(err)
	}
	if w := cfg.Warnings(); len(w) != 1 || !strings.Contains(w[0], "ROWBIRD_BASE_URL") {
		t.Errorf("warnings %v", w)
	}
}

func TestSQLiteDirs(t *testing.T) {
	cfg, err := Load(Options{Environ: environ("ROWBIRD_DATA_DIR=/srv/rowbird")})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SQLiteDirs) != 1 || cfg.SQLiteDirs[0] != "/srv/rowbird/sqlite" || cfg.InternalSQLitePath() != "/srv/rowbird/rowbird.db" {
		t.Fatalf("dirs %v internal %q", cfg.SQLiteDirs, cfg.InternalSQLitePath())
	}
	cfg, err = Load(Options{Environ: environ("ROWBIRD_SQLITE_DIRS=/a, /b/../c", "ROWBIRD_DATABASE_URL=postgres://x/y")})
	if err != nil || len(cfg.SQLiteDirs) != 2 || cfg.SQLiteDirs[1] != "/c" || cfg.InternalSQLitePath() != "" {
		t.Fatalf("dirs %v %v", cfg.SQLiteDirs, err)
	}
	if _, err := Load(Options{Environ: environ("ROWBIRD_SQLITE_DIRS=relative/dir")}); err == nil {
		t.Fatal("relative directory accepted")
	}
}

func TestStorageS3FromEnvironment(t *testing.T) {
	cfg, err := Load(Options{Environ: environ(
		"ROWBIRD_STORAGE_BACKEND=s3", "ROWBIRD_STORAGE_S3_ENDPOINT=https://s3.example.com", "ROWBIRD_STORAGE_S3_BUCKET=b",
		"ROWBIRD_STORAGE_S3_ACCESS_KEY=ak", "ROWBIRD_STORAGE_S3_SECRET_KEY=sk", "ROWBIRD_STORAGE_S3_PATH_STYLE=true",
	)})
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.StorageS3
	if s.Endpoint != "https://s3.example.com" || s.Bucket != "b" || s.AccessKey.Reveal() != "ak" || s.SecretKey.Reveal() != "sk" || !s.PathStyle {
		t.Errorf("s3 %+v", s)
	}
	if _, err := Load(Options{Environ: environ("ROWBIRD_STORAGE_BACKEND=s3")}); err == nil || !strings.Contains(err.Error(), "storage_s3") {
		t.Errorf("missing endpoint: %v", err)
	}
}
