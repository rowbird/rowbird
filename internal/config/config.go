// Package config loads the server configuration. Precedence, highest first: command-line flags,
// ROWBIRD_* environment variables, the `server` section of the config file, built-in defaults.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/spf13/pflag"

	"github.com/rowbird/rowbird/internal/logging"
	"github.com/rowbird/rowbird/internal/schedule"
	"github.com/rowbird/rowbird/internal/security"
)

// EnvPrefix is the prefix of every environment variable read by Rowbird.
const EnvPrefix = "ROWBIRD_"

// FlagConfigFile is the flag (and ROWBIRD_CONFIG_FILE the variable) naming the config file.
const FlagConfigFile = "config"

// Network policies.
const (
	NetworkPolicyOpen         = "open"
	NetworkPolicyBlockPrivate = "block-private"
)

// Config is the server configuration described in docs/spec/08-operations.md.
type Config struct {
	MasterKey     security.Secret `koanf:"master_key"`
	MasterKeyFile string          `koanf:"master_key_file"`
	// MasterKeyPrevious (or the file) is an older key accepted for decryption only, while a key
	// rotation is rolled out to several instances.
	MasterKeyPrevious     security.Secret `koanf:"master_key_previous"`
	MasterKeyPreviousFile string          `koanf:"master_key_previous_file"`
	DatabaseURL           security.Secret `koanf:"database_url"`
	DataDir               string          `koanf:"data_dir"`
	BaseURL               string          `koanf:"base_url"`
	ListenAddr            string          `koanf:"listen_addr"`
	Workers               int             `koanf:"workers"`
	SchedulerTick         time.Duration   `koanf:"scheduler_tick"`
	// ShutdownTimeout is how long running runs may take to finish when the server stops.
	ShutdownTimeout time.Duration `koanf:"shutdown_timeout"`
	// NoScheduler and NoWorkers disable those roles, for split deployments.
	NoScheduler    bool   `koanf:"no_scheduler"`
	NoWorkers      bool   `koanf:"no_workers"`
	StorageBackend string `koanf:"storage_backend"`
	// StorageS3 configures the s3 storage backend (ROWBIRD_STORAGE_S3_*).
	StorageS3      StorageS3       `koanf:"storage_s3"`
	LogLevel       string          `koanf:"log_level"`
	LogFormat      string          `koanf:"log_format"`
	MetricsToken   security.Secret `koanf:"metrics_token"`
	ConfigDir      string          `koanf:"config_dir"`
	NetworkPolicy  string          `koanf:"network_policy"`
	UpdateCheck    bool            `koanf:"update_check"`
	TrustedProxies []string        `koanf:"trusted_proxies"`
	SetupToken     security.Secret `koanf:"setup_token"`
	// SQLiteDirs are the directories the SQLite connector may open files from.
	SQLiteDirs []string `koanf:"sqlite_dirs"`
	// BackupSchedule is a cron expression (UTC) for scheduled backups of a SQLite store; empty
	// disables them. Backups go to BackupDir, which keeps the newest BackupKeep, and are also
	// uploaded to BackupS3 when it names a bucket.
	BackupSchedule      string    `koanf:"backup_schedule"`
	BackupDir           string    `koanf:"backup_dir"`
	BackupKeep          int       `koanf:"backup_keep"`
	BackupWithArtifacts bool      `koanf:"backup_with_artifacts"`
	BackupS3            StorageS3 `koanf:"backup_s3"`
}

// StorageS3 is an S3-compatible bucket for artifacts.
type StorageS3 struct {
	Endpoint  string          `koanf:"endpoint"`
	Region    string          `koanf:"region"`
	Bucket    string          `koanf:"bucket"`
	AccessKey security.Secret `koanf:"access_key"`
	SecretKey security.Secret `koanf:"secret_key"`
	PathStyle bool            `koanf:"path_style"`
	Prefix    string          `koanf:"prefix"`
}

// ArtifactsDir is where the local storage backend keeps artifacts.
func (c *Config) ArtifactsDir() string { return filepath.Join(c.DataDir, "artifacts") }

// defaults are applied before any other source. DatabaseURL is derived from DataDir when empty.
func defaults() map[string]any {
	return map[string]any{
		"data_dir":         "/data",
		"listen_addr":      ":8080",
		"workers":          4,
		"scheduler_tick":   "5s",
		"shutdown_timeout": "30s",
		"storage_backend":  "local",
		"log_level":        "info",
		"log_format":       "text",
		"network_policy":   NetworkPolicyOpen,
		"update_check":     true,
		"backup_keep":      7,
	}
}

// RegisterFlags adds the configuration flags to fs. Flag names are the keys in kebab case.
func RegisterFlags(fs *pflag.FlagSet) {
	fs.String(FlagConfigFile, "", "path to a rowbird.yaml file whose server section is loaded")
	fs.String("database-url", "", "internal store URL (sqlite://path or postgres://...)")
	fs.String("data-dir", "", "directory for the SQLite store, local artifacts and the generated key")
	fs.String("base-url", "", "public URL used in links sent in messages")
	fs.String("listen-addr", "", "HTTP listen address")
	fs.Int("workers", 0, "concurrent runs per instance")
	fs.Duration("scheduler-tick", 0, "scheduler polling interval")
	fs.Duration("shutdown-timeout", 0, "how long running runs may take to finish on shutdown")
	fs.Bool("no-scheduler", false, "do not schedule reports on this instance")
	fs.Bool("no-workers", false, "do not execute runs on this instance")
	fs.String("log-level", "", "debug, info, warn or error")
	fs.String("log-format", "", "text or json")
}

// Options control where Load reads from. Zero values read the real process environment.
type Options struct {
	Flags   *pflag.FlagSet
	Environ func() []string
}

// Load reads and validates the configuration.
func Load(opts Options) (*Config, error) {
	environ := opts.Environ
	if environ == nil {
		environ = os.Environ
	}
	k := koanf.New(".")
	if err := k.Load(confmap.Provider(defaults(), "."), nil); err != nil {
		return nil, fmt.Errorf("load defaults: %w", err)
	}

	if path := configFilePath(opts.Flags, environ); path != "" {
		fk := koanf.New(".")
		if err := fk.Load(file.Provider(path), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("read config file %s: %w", path, err)
		}
		if err := k.Load(confmap.Provider(fk.Cut("server").All(), "."), nil); err != nil {
			return nil, fmt.Errorf("apply config file %s: %w", path, err)
		}
	}

	envProvider := env.Provider(".", env.Opt{
		Prefix:      EnvPrefix,
		EnvironFunc: environ,
		TransformFunc: func(key, value string) (string, any) {
			key = strings.ToLower(strings.TrimPrefix(key, EnvPrefix))
			if key == "config_file" {
				return "", nil
			}
			if key == "trusted_proxies" || key == "sqlite_dirs" {
				return key, splitList(value)
			}
			if rest, ok := strings.CutPrefix(key, "storage_s3_"); ok {
				return "storage_s3." + rest, value
			}
			if rest, ok := strings.CutPrefix(key, "backup_s3_"); ok {
				return "backup_s3." + rest, value
			}
			return key, value
		},
	})
	if err := k.Load(envProvider, nil); err != nil {
		return nil, fmt.Errorf("read environment: %w", err)
	}

	if opts.Flags != nil {
		flagProvider := posflag.ProviderWithFlag(opts.Flags, ".", k, func(f *pflag.Flag) (string, any) {
			if !f.Changed || f.Name == FlagConfigFile {
				return "", nil
			}
			return strings.ReplaceAll(f.Name, "-", "_"), posflag.FlagVal(opts.Flags, f)
		})
		if err := k.Load(flagProvider, nil); err != nil {
			return nil, fmt.Errorf("read flags: %w", err)
		}
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("decode configuration: %w", err)
	}
	if err := cfg.finish(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func configFilePath(fs *pflag.FlagSet, environ func() []string) string {
	if fs != nil {
		if f := fs.Lookup(FlagConfigFile); f != nil && f.Changed {
			return f.Value.String()
		}
	}
	for _, kv := range environ() {
		if v, ok := strings.CutPrefix(kv, EnvPrefix+"CONFIG_FILE="); ok {
			return v
		}
	}
	return ""
}

func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// finish derives dependent values and validates the result.
func (c *Config) finish() error {
	if c.DataDir == "" {
		return errors.New("data_dir must not be empty")
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return fmt.Errorf("resolve data_dir: %w", err)
	}
	c.DataDir = abs
	if !c.DatabaseURL.IsSet() {
		c.DatabaseURL = security.Secret("sqlite://" + filepath.ToSlash(filepath.Join(abs, "rowbird.db")))
	}
	if c.BackupDir == "" {
		c.BackupDir = filepath.Join(abs, "backups")
	}
	if len(c.SQLiteDirs) == 0 {
		c.SQLiteDirs = []string{filepath.Join(abs, "sqlite")}
	}
	for i, d := range c.SQLiteDirs {
		if !filepath.IsAbs(d) {
			return fmt.Errorf("invalid configuration: sqlite_dirs entry %q must be an absolute path", d)
		}
		c.SQLiteDirs[i] = filepath.Clean(d)
	}
	return c.validate()
}

func (c *Config) validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if c.MasterKey.IsSet() && c.MasterKeyFile != "" {
		add("master_key and master_key_file are mutually exclusive")
	}
	if c.MasterKeyPrevious.IsSet() && c.MasterKeyPreviousFile != "" {
		add("master_key_previous and master_key_previous_file are mutually exclusive")
	}
	switch scheme, _, _ := strings.Cut(c.DatabaseURL.Reveal(), "://"); scheme {
	case "sqlite", "postgres", "postgresql":
	default:
		add("database_url must start with sqlite:// or postgres://")
	}
	if c.BaseURL != "" {
		u, err := url.Parse(c.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			add("base_url must be an absolute http or https URL, got %q", c.BaseURL)
		}
	}
	if c.ListenAddr == "" {
		add("listen_addr must not be empty")
	} else if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		add("listen_addr %q is not host:port", c.ListenAddr)
	}
	if c.Workers < 1 {
		add("workers must be at least 1, got %d", c.Workers)
	}
	if c.SchedulerTick < time.Second {
		add("scheduler_tick must be at least 1s, got %s", c.SchedulerTick)
	}
	if c.ShutdownTimeout < time.Second {
		add("shutdown_timeout must be at least 1s, got %s", c.ShutdownTimeout)
	}
	if c.StorageBackend != "local" && c.StorageBackend != "s3" {
		add("storage_backend must be local or s3, got %q", c.StorageBackend)
	}
	if c.StorageBackend == "s3" && (c.StorageS3.Endpoint == "" || c.StorageS3.Bucket == "") {
		add("storage_s3 needs an endpoint and a bucket when storage_backend is s3")
	}
	if c.BackupSchedule != "" {
		if _, err := schedule.Parse(c.BackupSchedule, "UTC"); err != nil {
			add("backup_schedule %q is not a valid cron expression", c.BackupSchedule)
		}
	}
	if c.BackupKeep < 1 {
		add("backup_keep must be at least 1, got %d", c.BackupKeep)
	}
	if (c.BackupS3.Endpoint == "") != (c.BackupS3.Bucket == "") {
		add("backup_s3 needs both an endpoint and a bucket")
	}
	if _, err := logging.ParseLevel(c.LogLevel); err != nil {
		errs = append(errs, err)
	}
	if c.LogFormat != logging.FormatText && c.LogFormat != logging.FormatJSON {
		add("log_format must be text or json, got %q", c.LogFormat)
	}
	if c.NetworkPolicy != NetworkPolicyOpen && c.NetworkPolicy != NetworkPolicyBlockPrivate {
		add("network_policy must be %s or %s, got %q", NetworkPolicyOpen, NetworkPolicyBlockPrivate, c.NetworkPolicy)
	}
	for _, p := range c.TrustedProxies {
		if _, _, err := net.ParseCIDR(p); err != nil {
			add("trusted_proxies entry %q is not a CIDR", p)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return nil
}

// TrustedProxyPrefixes parses TrustedProxies (already validated).
func (c *Config) TrustedProxyPrefixes() []netip.Prefix {
	out := make([]netip.Prefix, 0, len(c.TrustedProxies))
	for _, p := range c.TrustedProxies {
		if prefix, err := netip.ParsePrefix(p); err == nil {
			out = append(out, prefix.Masked())
		}
	}
	return out
}

// SpoolDir is where runs keep their result spools.
func (c *Config) SpoolDir() string { return filepath.Join(c.DataDir, "spool") }

// InternalSQLitePath returns the file of the internal store when it is SQLite, else "". The SQLite
// connector refuses to open it.
func (c *Config) InternalSQLitePath() string {
	if p, ok := strings.CutPrefix(c.DatabaseURL.Reveal(), "sqlite://"); ok {
		return filepath.FromSlash(p)
	}
	return ""
}

// SecureCookies reports whether cookies must carry the Secure attribute.
func (c *Config) SecureCookies() bool { return strings.HasPrefix(c.BaseURL, "https://") }

// Warnings lists non-fatal configuration problems worth logging at startup.
func (c *Config) Warnings() []string {
	var w []string
	if c.BaseURL == "" {
		w = append(w, "ROWBIRD_BASE_URL is not set; links in messages cannot be built until it is")
	}
	if strings.HasPrefix(c.BaseURL, "http://") {
		w = append(w, "ROWBIRD_BASE_URL uses http; session cookies are not marked Secure")
	}
	return w
}
