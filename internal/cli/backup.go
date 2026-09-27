package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/rowbird/rowbird/internal/backup"
	"github.com/rowbird/rowbird/internal/config"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/version"
)

// errPostgresBackup explains how Postgres stores are backed up.
var errPostgresBackup = errors.New("the internal store is Postgres: back it up with pg_dump (and restore with pg_restore); see the operations guide")

// liveWindow is how recent a heartbeat must be for restore to consider the server running.
const liveWindow = 2 * time.Minute

func backupCommand(env Env) *cobra.Command {
	var output string
	var withArtifacts bool
	cmd := &cobra.Command{
		Use:   "backup -o file",
		Short: "Write a backup of the SQLite store (online, without stopping the server)",
		Long: `Write a backup of the SQLite store: a .tar.gz with the database, a manifest and, with
--with-artifacts, the files in local artifact storage. Use "-o -" to write to standard output.

The master key is not included. Back it up separately: the secrets in a backup cannot be read
without it. Postgres stores are backed up with pg_dump.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if output == "" {
				return errors.New("name the backup file with -o (or -o - for standard output)")
			}
			cfg, logger, err := load(cmd, env)
			if err != nil {
				return err
			}
			if cfg.InternalSQLitePath() == "" {
				return errPostgresBackup
			}
			st, err := store.Open(ctx, cfg.DatabaseURL, logger)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			opts := backup.Options{KeyID: currentKeyID(cfg), Version: version.Get().Version}
			if withArtifacts {
				if cfg.StorageBackend != "local" {
					return errors.New("--with-artifacts only applies to local artifact storage; artifacts in S3 stay in the bucket")
				}
				opts.ArtifactsDir = cfg.ArtifactsDir()
			}

			w := cmd.OutOrStdout()
			var tmp *os.File
			if output != "-" {
				// The image has no shell to create a directory with, so create missing ones here.
				if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
					return err
				}
				if tmp, err = os.CreateTemp(filepath.Dir(output), ".rowbird-backup-*"); err != nil {
					return err
				}
				defer func() { _ = os.Remove(tmp.Name()) }()
				w = tmp
			}
			m, err := backup.Write(ctx, st, w, opts)
			if err != nil {
				if tmp != nil {
					_ = tmp.Close()
				}
				return err
			}
			if tmp == nil {
				return nil
			}
			if err := errors.Join(tmp.Sync(), tmp.Close(), os.Chmod(tmp.Name(), 0o600)); err != nil {
				return err
			}
			if err := os.Rename(tmp.Name(), output); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "backup written to %s (schema %d, key %s); keep the master key backed up separately\n", output, m.Schema, m.KeyID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "backup file to write, or - for standard output")
	cmd.Flags().BoolVar(&withArtifacts, "with-artifacts", false, "include the files in local artifact storage")
	return cmd
}

func restoreCommand(env Env) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "restore file",
		Short: "Replace the SQLite store with a backup (stop the server first)",
		Long: `Replace the SQLite store with a backup written by "rowbird backup". Stop the server first.
The current database is kept next to it with a .pre-restore suffix. A backup from a newer Rowbird is
refused. Start the server with the master key the backup was taken with.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			cfg, logger, err := load(cmd, env)
			if err != nil {
				return err
			}
			dbPath := cfg.InternalSQLitePath()
			if dbPath == "" {
				return errPostgresBackup
			}
			if _, err := backup.ReadManifest(args[0]); err != nil {
				return err
			}
			st, err := store.Open(ctx, cfg.DatabaseURL, logger)
			if err != nil {
				return err
			}
			status, err := st.MigrationStatus(ctx)
			if err != nil {
				_ = st.Close()
				return err
			}
			if !force && status.Current > 0 {
				live, err := st.System().LiveInstances(ctx, time.Now().Add(-liveWindow))
				if err != nil {
					_ = st.Close()
					return err
				}
				if len(live) > 0 {
					_ = st.Close()
					return fmt.Errorf("a server (%s on %s) was running within the last %s; stop it first, or pass --force if it is gone", live[0].ID, live[0].Hostname, liveWindow)
				}
			}
			if err := st.Close(); err != nil {
				return err
			}
			res, err := backup.Restore(ctx, args[0], backup.RestoreOptions{
				DatabasePath: dbPath, ArtifactsDir: cfg.ArtifactsDir(), LatestSchema: status.Latest, KeyID: currentKeyID(cfg),
			})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "restored the backup taken at %s (Rowbird %s, schema %d", res.Manifest.CreatedAt.Format(time.RFC3339), res.Manifest.Version, res.Manifest.Schema)
			if res.Artifacts > 0 {
				_, _ = fmt.Fprintf(out, ", %d artifacts", res.Artifacts)
			}
			_, _ = fmt.Fprintln(out, ")")
			if res.Previous != "" {
				_, _ = fmt.Fprintf(out, "the previous database was kept as %s\n", res.Previous)
			}
			if res.KeyMismatch {
				_, _ = fmt.Fprintf(out, "warning: the backup was taken with master key %s, not the configured one; start the server with that key, or stored secrets cannot be read\n", res.Manifest.KeyID)
			}
			if res.Manifest.Schema < status.Latest {
				_, _ = fmt.Fprintln(out, "the server will migrate the restored database when it starts")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "restore even though a server looked alive recently")
	return cmd
}

// currentKeyID is the id of the configured master key, without generating one: "" when the data
// directory has no key yet.
func currentKeyID(cfg *config.Config) string {
	raw, err := crypto.LoadKey(cfg.MasterKey, cfg.MasterKeyFile)
	if err == nil && raw == nil {
		raw, err = crypto.LoadKey("", filepath.Join(cfg.DataDir, crypto.GeneratedKeyFile))
	}
	if err != nil || raw == nil {
		return ""
	}
	return crypto.KeyID(raw)
}
