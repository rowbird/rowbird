package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/i18n"
	"github.com/rowbird/rowbird/internal/store"
)

// userCommand groups the local administration commands (docs/spec/08-operations.md, "CLI"). They
// talk to the store directly, so an operator can recover access without a working admin account.
// Security events are recorded without an actor, which marks them as system or CLI actions.
func userCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Manage users directly in the local store (admin recovery)"}
	cmd.AddCommand(userCreateCommand(env), userResetPasswordCommand(env), userDisable2FACommand(env), userSetRoleCommand(env))
	return cmd
}

// withService opens the store and the identity service, bound to the default workspace.
func withService(cmd *cobra.Command, env Env, fn func(ctx context.Context, svc *auth.Service, st *store.Store) error) error {
	ctx := cmd.Context()
	cfg, logger, err := load(cmd, env)
	if err != nil {
		return err
	}
	kr, _, err := loadKeyring(cfg)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if _, err := st.Migrate(ctx); err != nil {
		return err
	}
	ws, err := st.Workspaces().GetBySlug(ctx, auth.DefaultWorkspaceSlug)
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("setup has not been completed yet; open the web UI to create the first admin")
	}
	if err != nil {
		return err
	}
	svc := auth.NewService(st, kr, auth.Config{}, auth.Options{Logger: logger})
	return fn(store.WithWorkspace(ctx, ws.ID), svc, st)
}

// findUser resolves --email to a member of the default workspace.
func findUser(ctx context.Context, svc *auth.Service, st *store.Store, email string) (*auth.UserWithRole, error) {
	u, err := st.Users().GetByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("no user with email %q", email)
	}
	if err != nil {
		return nil, err
	}
	return svc.GetUser(ctx, u.ID)
}

// explain turns domain errors into the English message from the server catalog.
func explain(err error) error {
	if de, ok := apperr.As(err); ok {
		msg := i18n.Default().T("en", "errors."+de.Code)
		for _, f := range de.Fields {
			msg += fmt.Sprintf("\n  %s: %s", f.Field, f.Code)
		}
		return fmt.Errorf("%s (%s)", msg, de.Code)
	}
	return err
}

var cliMeta = auth.RequestMeta{UserAgent: "rowbird-cli"}

func userCreateCommand(env Env) *cobra.Command {
	var email, name, role, locale string
	var passwordStdin bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a user; prints a temporary password unless --password-stdin is used",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var password string
			if passwordStdin {
				line, err := bufio.NewReader(env.Stdin).ReadString('\n')
				if err != nil && line == "" {
					return errors.New("--password-stdin: no password on standard input")
				}
				password = strings.TrimRight(line, "\r\n")
			}
			return withService(cmd, env, func(ctx context.Context, svc *auth.Service, _ *store.Store) error {
				u, temp, err := svc.CreateUser(ctx, nil, auth.NewUserInput{Email: email, Name: name, Role: store.Role(role), Locale: locale, Password: password}, cliMeta)
				if err != nil {
					return explain(err)
				}
				out := cmd.OutOrStdout()
				_, _ = fmt.Fprintf(out, "created %s with role %s\n", u.User.Email, u.Role)
				if !passwordStdin {
					_, _ = fmt.Fprintf(out, "temporary password: %s\nThe user must change it at the first sign-in.\n", temp)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "email address (required)")
	cmd.Flags().StringVar(&name, "name", "", "display name (required)")
	cmd.Flags().StringVar(&role, "role", "viewer", "admin, editor or viewer")
	cmd.Flags().StringVar(&locale, "locale", "", "en or pt-BR (default: workspace default)")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the password from standard input instead of generating a temporary one")
	_ = cmd.MarkFlagRequired("email")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func userResetPasswordCommand(env Env) *cobra.Command {
	var email string
	cmd := &cobra.Command{
		Use:   "reset-password",
		Short: "Give a user a new temporary password, unlock the account and sign it out everywhere",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withService(cmd, env, func(ctx context.Context, svc *auth.Service, st *store.Store) error {
				u, err := findUser(ctx, svc, st, email)
				if err != nil {
					return err
				}
				temp, err := svc.ResetPassword(ctx, nil, u.User.ID, cliMeta)
				if err != nil {
					return explain(err)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "temporary password for %s: %s\nThe user must change it at the next sign-in.\n", u.User.Email, temp)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "email address (required)")
	_ = cmd.MarkFlagRequired("email")
	return cmd
}

func userDisable2FACommand(env Env) *cobra.Command {
	var email string
	cmd := &cobra.Command{
		Use:   "disable-2fa",
		Short: "Turn off a user's two-factor authentication",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withService(cmd, env, func(ctx context.Context, svc *auth.Service, st *store.Store) error {
				u, err := findUser(ctx, svc, st, email)
				if err != nil {
					return err
				}
				if err := svc.AdminDisableTOTP(ctx, nil, u.User.ID, cliMeta); err != nil {
					return explain(err)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "two-factor authentication is off for %s\n", u.User.Email)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "email address (required)")
	_ = cmd.MarkFlagRequired("email")
	return cmd
}

func userSetRoleCommand(env Env) *cobra.Command {
	var email, role string
	cmd := &cobra.Command{
		Use:   "set-role",
		Short: "Change a user's role (the last active admin cannot be demoted)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withService(cmd, env, func(ctx context.Context, svc *auth.Service, st *store.Store) error {
				u, err := findUser(ctx, svc, st, email)
				if err != nil {
					return err
				}
				if err := svc.SetRole(ctx, u.User.ID, store.Role(role), cliMeta); err != nil {
					return explain(err)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is now %s\n", u.User.Email, role)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "email address (required)")
	cmd.Flags().StringVar(&role, "role", "", "admin, editor or viewer (required)")
	_ = cmd.MarkFlagRequired("email")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}
