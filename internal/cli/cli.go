// Package cli implements the rowbird command line (docs/spec/08-operations.md, "CLI").
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/rowbird/rowbird/internal/app"
	"github.com/rowbird/rowbird/internal/config"
	"github.com/rowbird/rowbird/internal/logging"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/version"
)

// Env gives commands their process context, so tests can run them in-process.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Environ        func() []string
	// SPA serves the web UI in `serve`. Nil serves only the API.
	SPA http.Handler
}

// NewRootCommand builds the command tree. Running it without a subcommand runs `serve`.
func NewRootCommand(env Env) *cobra.Command {
	if env.Environ == nil {
		env.Environ = os.Environ
	}
	if env.Stdin == nil {
		env.Stdin = os.Stdin
	}
	root := &cobra.Command{
		Use:           "rowbird",
		Short:         "Rowbird: your SQL results, delivered",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	config.RegisterFlags(root.PersistentFlags())

	serve := serveCommand(env)
	root.RunE = serve.RunE
	root.AddCommand(serve, migrateCommand(env), versionCommand(env), healthcheckCommand(env), userCommand(env), exportCommand(env), applyCommand(env), keysCommand(env), backupCommand(env), restoreCommand(env))
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context, args []string, env Env) int {
	root := NewRootCommand(env)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "error:", logging.RedactString(err.Error()))
		return 1
	}
	return 0
}

func load(cmd *cobra.Command, env Env) (*config.Config, *slog.Logger, error) {
	cfg, err := config.Load(config.Options{Flags: flagsOf(cmd), Environ: env.Environ})
	if err != nil {
		return nil, nil, err
	}
	logger, err := logging.New(env.Stderr, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return nil, nil, err
	}
	return cfg, logger, nil
}

func flagsOf(cmd *cobra.Command) *pflag.FlagSet {
	fs := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
	fs.AddFlagSet(cmd.InheritedFlags())
	fs.AddFlagSet(cmd.LocalFlags())
	return fs
}

func serveCommand(env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the server (API, UI, scheduler and workers); the default command",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, logger, err := load(cmd, env)
			if err != nil {
				return err
			}
			a, err := app.New(ctx, cfg, logger, app.Options{SPA: env.SPA})
			if err != nil {
				return err
			}
			var lc net.ListenConfig
			ln, err := lc.Listen(ctx, "tcp", cfg.ListenAddr)
			if err != nil {
				return fmt.Errorf("listen on %s: %w", cfg.ListenAddr, err)
			}
			return a.Serve(ctx, ln)
		},
	}
}

func migrateCommand(env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending migrations to the internal store and exit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, logger, err := load(cmd, env)
			if err != nil {
				return err
			}
			st, err := store.Open(ctx, cfg.DatabaseURL, logger)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			res, err := st.Migrate(ctx)
			if err != nil {
				return err
			}
			if len(res.Applied) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "schema is up to date at version %d\n", res.To)
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "migrated schema from version %d to %d (%d migrations)\n", res.From, res.To, len(res.Applied))
			if res.BackupPath != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "backup written to %s\n", res.BackupPath)
			}
			return nil
		},
	}
}

func versionCommand(env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version.Get())
			return err
		},
	}
}

func healthcheckCommand(env Env) *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Exit 0 when the local server is ready, 1 otherwise (for Docker HEALTHCHECK)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if target == "" {
				cfg, err := config.Load(config.Options{Flags: flagsOf(cmd), Environ: env.Environ})
				if err != nil {
					return err
				}
				target = readyURL(cfg.ListenAddr)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("health check failed: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("health check failed: %s returned %d", target, resp.StatusCode)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "ok")
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "url", "", "readiness URL to probe (default derived from the listen address)")
	return cmd
}

// readyURL points at the readiness endpoint of a server listening on addr, from the same host.
func readyURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8080/health/ready"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/health/ready"
}
