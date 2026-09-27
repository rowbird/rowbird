package cli

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rowbird/rowbird/internal/config"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/keys"
	"github.com/rowbird/rowbird/internal/store"
)

// loadKeyring loads the master key and, when configured, the previous one (decryption only).
func loadKeyring(cfg *config.Config) (*crypto.Keyring, *crypto.MasterKey, error) {
	mk, err := crypto.LoadMasterKey(crypto.MasterKeySource{Value: cfg.MasterKey, File: cfg.MasterKeyFile, DataDir: cfg.DataDir})
	if err != nil {
		return nil, nil, fmt.Errorf("load master key: %w", err)
	}
	previous, err := crypto.LoadKey(cfg.MasterKeyPrevious, cfg.MasterKeyPreviousFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load previous master key: %w", err)
	}
	var older [][]byte
	if previous != nil {
		older = append(older, previous)
	}
	kr, err := crypto.NewKeyring(mk.Raw, older...)
	if err != nil {
		return nil, nil, err
	}
	return kr, mk, nil
}

func keysCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{Use: "keys", Short: "Manage the master key that encrypts stored secrets"}
	cmd.AddCommand(keysRotateCommand(env))
	return cmd
}

// keysRotateCommand encrypts every stored secret again with a new master key. With the key the
// server generated in the data directory, the command also replaces that file (keeping the old
// key next to it); with a configured key the operator points the configuration at the new one.
func keysRotateCommand(env Env) *cobra.Command {
	var newKeyFile string
	var dryRun, force bool
	cmd := &cobra.Command{
		Use:   "rotate",
		Short: "Encrypt every stored secret again with a new master key",
		Long: `Encrypt every stored secret again with a new master key, in one transaction.

With a single instance, stop the server, rotate, then start it with the new key. With several
instances on Postgres, first restart them with the new key as ROWBIRD_MASTER_KEY and the current
one as ROWBIRD_MASTER_KEY_PREVIOUS, then rotate, then remove the previous key.

Signed-in users are asked to sign in again, because request forgery tokens derive from the key.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			cfg, logger, err := load(cmd, env)
			if err != nil {
				return err
			}
			kr, mk, err := loadKeyring(cfg)
			if err != nil {
				return err
			}
			generated := !cfg.MasterKey.IsSet() && cfg.MasterKeyFile == ""
			var newKey []byte
			switch {
			case newKeyFile != "":
				if newKey, err = crypto.LoadKey("", newKeyFile); err != nil {
					return fmt.Errorf("--new-key-file: %w", err)
				}
			case generated:
				if newKey, err = crypto.NewRawKey(); err != nil {
					return err
				}
			default:
				return errors.New("the master key comes from the configuration: pass the new key with --new-key-file")
			}
			if crypto.KeyID(newKey) == kr.PrimaryKeyID() {
				return errors.New("the new key is the key already in use")
			}

			st, err := store.Open(ctx, cfg.DatabaseURL, logger)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			if _, err := st.Migrate(ctx); err != nil {
				return err
			}
			// With the generated key, the new key is on disk before the secrets use it, so a crash
			// in between never leaves values encrypted with a key that exists nowhere.
			next := mk.Path + ".next"
			if generated && !dryRun {
				if err := crypto.WriteKeyFile(next, newKey); err != nil {
					return err
				}
			}
			res, err := keys.Rotate(ctx, st, kr, newKey, keys.Options{DryRun: dryRun, Force: force})
			if err != nil {
				if generated && !dryRun {
					_ = os.Remove(next)
				}
				return err
			}
			verb := "encrypted"
			if dryRun {
				verb = "would encrypt"
			}
			_, _ = fmt.Fprintf(out, "%s %d values with key %s (%s); %d already used it\n", verb, res.Total(), res.NewKey, counts(res.Rotated), res.Current)
			if dryRun {
				return nil
			}
			if generated {
				previous := mk.Path + ".previous"
				if err := crypto.WriteKeyFile(previous, mk.Raw); err != nil {
					return fmt.Errorf("the secrets now use the new key, but saving the old key failed: %w", err)
				}
				if err := os.Rename(next, mk.Path); err != nil {
					return fmt.Errorf("the secrets now use the new key in %s, but moving it to %s failed; move it before starting the server: %w", next, mk.Path, err)
				}
				_, _ = fmt.Fprintf(out, "wrote the new key to %s and kept the old one in %s; back up the new key now\n", mk.Path, filepath.Base(previous))
				return nil
			}
			_, _ = fmt.Fprintln(out, "now configure the new key (ROWBIRD_MASTER_KEY or ROWBIRD_MASTER_KEY_FILE), back it up, and restart the server")
			return nil
		},
	}
	cmd.Flags().StringVar(&newKeyFile, "new-key-file", "", "file holding the new base64 key (generated when the key lives in the data directory)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "decrypt and encrypt every value, then roll back")
	cmd.Flags().BoolVar(&force, "force", false, "rotate even while instances that use another key are running")
	return cmd
}

func counts(m map[string]int) string {
	if len(m) == 0 {
		return "nothing to do"
	}
	parts := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, fmt.Sprintf("%s: %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}
