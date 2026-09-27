// Package app wires the server together: configuration, logging, secrets, store and HTTP.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/rowbird/rowbird/internal/gitops"

	"github.com/rowbird/rowbird/internal/ai"

	"github.com/rowbird/rowbird/internal/api"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/backup"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/config"
	"github.com/rowbird/rowbird/internal/connections"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/delivery"
	"github.com/rowbird/rowbird/internal/links"
	"github.com/rowbird/rowbird/internal/maintenance"
	"github.com/rowbird/rowbird/internal/metrics"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/notify"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/reports"
	"github.com/rowbird/rowbird/internal/runner"
	"github.com/rowbird/rowbird/internal/scheduler"
	localstorage "github.com/rowbird/rowbird/internal/storage/local"
	s3storage "github.com/rowbird/rowbird/internal/storage/s3"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/updatecheck"
	"github.com/rowbird/rowbird/internal/version"
)

// ShutdownTimeout bounds how long in-flight requests may take once shutdown starts.
const ShutdownTimeout = 30 * time.Second

// App is a running Rowbird instance.
type App struct {
	cfg       *config.Config
	logger    *slog.Logger
	store     *store.Store
	keyring   *crypto.Keyring
	planner   *gitops.Planner
	exporter  *gitops.Exporter
	handler   http.Handler
	scheduler *scheduler.Scheduler
	runner    *runner.Runner
	notify    *notify.Service
	backups   *backup.Scheduler
	retention *maintenance.Retention
	updates   *updatecheck.Checker
}

// Options carry optional collaborators.
type Options struct {
	// SPA serves the embedded web UI. Nil serves only the API.
	SPA http.Handler
}

// New loads the master key, opens the store and applies pending migrations.
func New(ctx context.Context, cfg *config.Config, logger *slog.Logger, opts Options) (*App, error) {
	for _, w := range cfg.Warnings() {
		logger.WarnContext(ctx, "configuration warning", "warning", w)
	}

	mk, err := crypto.LoadMasterKey(crypto.MasterKeySource{Value: cfg.MasterKey, File: cfg.MasterKeyFile, DataDir: cfg.DataDir})
	if err != nil {
		return nil, fmt.Errorf("load master key: %w", err)
	}
	if mk.Generated {
		logger.WarnContext(ctx, "generated a new master key; back it up now, encrypted credentials cannot be recovered without it",
			"path", mk.Path)
	}
	previous, err := crypto.LoadKey(cfg.MasterKeyPrevious, cfg.MasterKeyPreviousFile)
	if err != nil {
		return nil, fmt.Errorf("load previous master key: %w", err)
	}
	var older [][]byte
	if previous != nil {
		older = append(older, previous)
		logger.InfoContext(ctx, "a previous master key is accepted for decryption", "previous_key_id", crypto.KeyID(previous))
	}
	keyring, err := crypto.NewKeyring(mk.Raw, older...)
	if err != nil {
		return nil, fmt.Errorf("load master key: %w", err)
	}

	st, err := store.Open(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return nil, err
	}
	if _, err := st.Migrate(ctx); err != nil {
		_ = st.Close()
		return nil, err
	}

	authSvc := auth.NewService(st, keyring, auth.Config{SetupToken: cfg.SetupToken, BaseURL: cfg.BaseURL}, auth.Options{
		Logger: logger, HTTPClient: netx.HTTPClient(netx.NewDialer(cfg.NetworkPolicy).DialContext, 15*time.Second),
	})
	if status, err := authSvc.SetupStatus(ctx); err != nil {
		_ = st.Close()
		return nil, err
	} else if status.Required && !status.TokenRequired {
		logger.WarnContext(ctx, "setup is pending: whoever opens the web UI first becomes the admin; set ROWBIRD_SETUP_TOKEN to require a token")
	}

	var forbidden []string
	if p := cfg.InternalSQLitePath(); p != "" {
		forbidden = append(forbidden, p)
	}
	connSvc := connections.NewService(st, keyring, connections.Options{
		Dial:           netx.NewDialer(cfg.NetworkPolicy).DialContext,
		SQLiteDirs:     cfg.SQLiteDirs,
		ForbiddenPaths: forbidden,
		Logger:         logger,
	})

	querySvc := queries.NewService(st, connSvc, queries.Options{Logger: logger})
	channelSvc := channels.NewService(st, keyring, channels.Options{Dial: netx.NewDialer(cfg.NetworkPolicy).DialContext, Logger: logger})
	aiSvc := ai.NewService(st, keyring, connSvc, ai.Options{Dial: netx.NewDialer(cfg.NetworkPolicy).DialContext, Logger: logger})
	notifySvc := notify.New(st, keyring, channelSvc, notify.Options{BaseURL: cfg.BaseURL, Logger: logger, Dial: netx.NewDialer(cfg.NetworkPolicy).DialContext})
	authSvc.SetResetMailer(notifySvc)
	meters := metrics.New(st.System(), version.Get().Version, logger)
	channelSvc.Observe(notifySvc)

	artifacts, err := openStorage(cfg)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	if cfg.BaseURL == "" {
		logger.WarnContext(ctx, "ROWBIRD_BASE_URL is not set: messages carry no links, and deliveries in link mode fail")
	}
	engine := delivery.New(st, channelSvc, delivery.Options{
		Storage: artifacts, Backend: cfg.StorageBackend, BaseURL: cfg.BaseURL, SpoolDir: cfg.SpoolDir(), Logger: logger, Notifier: notifySvc, Metrics: meters,
	})

	hostname, _ := os.Hostname()
	run := runner.New(runner.Config{
		Workers: cfg.Workers, Tick: cfg.SchedulerTick, SpoolDir: cfg.SpoolDir(), ShutdownTimeout: cfg.ShutdownTimeout,
		Hostname: hostname, Version: version.Get().Version, KeyID: keyring.PrimaryKeyID(), RecoverOnStart: st.Dialect() == store.SQLite,
	}, st, querySvc, connSvc, runner.Options{Logger: logger, Delivery: engine, Notifier: notifySvc, Metrics: meters})
	reportSvc := reports.NewService(st, querySvc, reports.Options{Logger: logger, Wake: run.Wake, CancelLocal: run.Cancel, SpoolDir: cfg.SpoolDir(), BaseURL: cfg.BaseURL, Deliverer: engine, Storage: artifacts, Notifier: notifySvc})

	a := &App{cfg: cfg, logger: logger, store: st, keyring: keyring, runner: run, notify: notifySvc, planner: gitops.NewPlanner(st, connSvc, channelSvc, querySvc, reportSvc), exporter: gitops.NewExporter(st, connSvc, channelSvc)}
	a.updates = updatecheck.New(st, updatecheck.Options{Allowed: cfg.UpdateCheck, Current: version.Get().Version, Logger: logger})
	a.retention = maintenance.New(st, maintenance.Options{Storage: artifacts, Backend: cfg.StorageBackend, Logger: logger})
	if cfg.BackupSchedule != "" {
		if a.backups, err = newBackupScheduler(cfg, st, keyring, notifySvc, logger); err != nil {
			if !errors.Is(err, store.ErrNotSQLite) {
				_ = st.Close()
				return nil, err
			}
			logger.WarnContext(ctx, "ROWBIRD_BACKUP_SCHEDULE is ignored: scheduled backups need a SQLite store; back up Postgres with pg_dump")
		}
	}
	if !cfg.NoScheduler {
		a.scheduler = scheduler.New(st, scheduler.Options{Logger: logger, Tick: cfg.SchedulerTick, Wake: run.Wake, Metrics: meters})
	}
	var schedHealth api.SchedulerHealth
	if a.scheduler != nil {
		schedHealth = a.scheduler
	}
	a.handler = api.NewRouter(api.Deps{
		Logger:         logger,
		Store:          st,
		Auth:           authSvc,
		Connections:    connSvc,
		Queries:        querySvc,
		Reports:        reportSvc,
		Channels:       channelSvc,
		Links:          links.NewService(st, artifacts, links.Options{Logger: logger}),
		Notify:         notifySvc,
		AI:             aiSvc,
		Planner:        a.planner,
		Exporter:       a.exporter,
		Metrics:        meters,
		MetricsToken:   cfg.MetricsToken.Reveal(),
		Scheduler:      schedHealth,
		MasterKey:      masterKeyInfo(cfg, mk),
		TrustedProxies: cfg.TrustedProxyPrefixes(),
		SecureCookies:  cfg.SecureCookies(),
		System:         &api.SystemInfo{Store: st, StorageBackend: cfg.StorageBackend, Backups: backupStatus(a.backups), Updates: a.updates},
		SPA:            opts.SPA,
	})
	return a, nil
}

// openStorage opens the artifact storage chosen by ROWBIRD_STORAGE_BACKEND.
func openStorage(cfg *config.Config) (plugin.Storage, error) {
	if cfg.StorageBackend == "s3" {
		c := cfg.StorageS3
		return s3storage.New(s3storage.Config{
			Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, AccessKey: c.AccessKey, SecretKey: c.SecretKey,
			PathStyle: c.PathStyle, Prefix: c.Prefix,
		})
	}
	return localstorage.New(cfg.ArtifactsDir())
}

// masterKeyInfo tells the setup wizard where the key came from, so it can insist on a backup when
// Rowbird generated it.
func masterKeyInfo(cfg *config.Config, mk *crypto.MasterKey) api.MasterKeyInfo {
	switch {
	case cfg.MasterKey.IsSet():
		return api.MasterKeyInfo{Source: "env"}
	case cfg.MasterKeyFile != "":
		return api.MasterKeyInfo{Source: "file"}
	}
	return api.MasterKeyInfo{Source: "generated", Path: mk.Path}
}

// Handler returns the root HTTP handler.
func (a *App) Handler() http.Handler { return a.handler }

// Serve accepts connections on ln until ctx is cancelled, then shuts down gracefully and closes
// the store.
func (a *App) Serve(ctx context.Context, ln net.Listener) error {
	defer func() { _ = a.store.Close() }()

	srv := &http.Server{
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(a.logger.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	if a.cfg.ConfigDir != "" {
		a.applyConfigDir(ctx)
	}
	info := version.Get()
	a.logger.InfoContext(ctx, "rowbird started",
		"version", info.Version, "addr", ln.Addr().String(), "store", string(a.store.Dialect()),
		"key_id", a.keyring.PrimaryKeyID())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	// The scheduler and the workers stop when ctx is cancelled; the workers then give running runs
	// the shutdown timeout to finish.
	bgCtx, stopBackground := context.WithCancel(ctx)
	var background sync.WaitGroup
	defer func() {
		stopBackground()
		background.Wait()
	}()
	if a.scheduler != nil {
		background.Go(func() { a.scheduler.Run(bgCtx) })
		// Only instances that schedule vouch for the whole service.
		background.Go(func() { a.notify.Heartbeat(bgCtx, a.healthy) })
	}
	background.Go(func() { a.notify.Run(bgCtx) })
	if a.backups != nil {
		background.Go(func() { a.backups.Run(bgCtx) })
	}
	background.Go(func() { a.retention.Run(bgCtx) })
	background.Go(func() { a.updates.Run(bgCtx) })
	if !a.cfg.NoWorkers {
		background.Go(func() {
			if err := a.runner.Run(bgCtx); err != nil {
				a.logger.ErrorContext(ctx, "runner stopped with an error", "error", err)
			}
		})
	}

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	a.logger.InfoContext(ctx, "shutting down")
	// Event streams never end on their own; closing the broker ends them so Shutdown can finish.
	a.notify.Events().Close()
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	a.logger.InfoContext(ctx, "stopped")
	return nil
}

// healthy reports whether the store answers and the scheduler is ticking, for the heartbeat.
func (a *App) healthy(ctx context.Context) bool {
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return a.store.Ping(pctx) == nil && a.scheduler != nil && a.scheduler.Healthy()
}

func newBackupScheduler(cfg *config.Config, st *store.Store, kr *crypto.Keyring, n *notify.Service, logger *slog.Logger) (*backup.Scheduler, error) {
	sc := backup.SchedulerConfig{
		Schedule: cfg.BackupSchedule, Dir: cfg.BackupDir, Keep: cfg.BackupKeep, WithArtifacts: cfg.BackupWithArtifacts,
		ArtifactsDir: cfg.ArtifactsDir(), KeyID: kr.PrimaryKeyID(), Version: version.Get().Version,
	}
	if b := cfg.BackupS3; b.Bucket != "" {
		sc.S3 = &s3storage.Config{Endpoint: b.Endpoint, Region: b.Region, Bucket: b.Bucket, AccessKey: b.AccessKey, SecretKey: b.SecretKey, PathStyle: b.PathStyle, Prefix: b.Prefix}
	}
	return backup.NewScheduler(st, sc, backup.SchedulerOptions{Logger: logger, Notifier: n})
}

// backupStatus avoids handing the API a typed nil when scheduled backups are off.
func backupStatus(b *backup.Scheduler) interface{ Status() backup.Status } {
	if b == nil {
		return nil
	}
	return b
}
