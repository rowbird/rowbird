package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rowbird/rowbird/internal/app"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/gitops"
)

// errPlanBlocked makes apply exit with 1 when the plan has problems (already printed).
var errPlanBlocked = errors.New("the documents were not applied; see the plan above")

// remote holds --server and --api-key, for export and apply through the API.
type remote struct {
	server, apiKey string
}

func (r *remote) flags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&r.server, "server", "", "a Rowbird URL to work through the API instead of the local store")
	cmd.Flags().StringVar(&r.apiKey, "api-key", "", "API key for --server (default: $ROWBIRD_API_KEY)")
}

func (r *remote) key(env Env) string {
	if r.apiKey != "" {
		return r.apiKey
	}
	return lookupEnv(env)("ROWBIRD_API_KEY")
}

func (r *remote) post(ctx context.Context, env Env, path string, body any) ([]byte, error) {
	key := r.key(env)
	if key == "" {
		return nil, errors.New("--server needs an API key: --api-key or ROWBIRD_API_KEY")
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.server, "/")+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req) //nolint:gosec // the operator names the server
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		var p struct {
			Code, Title string
		}
		_ = json.Unmarshal(out, &p)
		return nil, fmt.Errorf("%s answered %d: %s (%s)", r.server, res.StatusCode, p.Title, p.Code)
	}
	return out, nil
}

func lookupEnv(env Env) func(string) string {
	vars := map[string]string{}
	for _, kv := range env.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	return func(name string) string { return vars[name] }
}

// local opens the application on the local store, bound to the workspace and an admin.
func local(cmd *cobra.Command, env Env, fn func(ctx context.Context, a *app.App, p *auth.Principal) error) error {
	cfg, logger, err := load(cmd, env)
	if err != nil {
		return err
	}
	a, err := app.New(cmd.Context(), cfg, logger, app.Options{})
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx, p, err := a.AdminContext(cmd.Context())
	if err != nil {
		return err
	}
	return fn(ctx, a, p)
}

func exportCommand(env Env) *cobra.Command {
	var out string
	var sel gitops.Selection
	var r remote
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write reports, queries, connections and channels as YAML (secrets become ${env:} placeholders)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var data []byte
			if r.server != "" {
				body := map[string]any{"reports": sel.Reports, "queries": sel.Queries, "include_connections": sel.IncludeConnections, "include_channels": sel.IncludeChannels}
				var err error
				if data, err = r.post(cmd.Context(), env, "/api/v1/export", body); err != nil {
					return err
				}
			} else if err := local(cmd, env, func(ctx context.Context, a *app.App, _ *auth.Principal) error {
				docs, err := a.Exporter().Export(ctx, sel)
				if err != nil {
					return err
				}
				data, err = gitops.Encode(docs)
				return err
			}); err != nil {
				return err
			}
			if out == "" || out == "-" {
				_, err := cmd.OutOrStdout().Write(data)
				return err
			}
			return os.WriteFile(out, data, 0o600)
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "file to write (default: standard output)")
	cmd.Flags().StringSliceVar(&sel.Reports, "report", nil, "a report slug to export, with its query (repeatable; default: everything)")
	cmd.Flags().StringSliceVar(&sel.Queries, "query", nil, "a query slug to export (repeatable)")
	cmd.Flags().BoolVar(&sel.IncludeConnections, "with-connections", false, "include the connections used")
	cmd.Flags().BoolVar(&sel.IncludeChannels, "with-channels", false, "include the channels used")
	r.flags(cmd)
	return cmd
}

func applyCommand(env Env) *cobra.Command {
	var path, policy string
	var dryRun bool
	var maps []string
	var r remote
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Create or update resources from YAML documents (a file or a directory)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !gitops.ValidPolicy(gitops.Policy(policy)) {
				return fmt.Errorf("--policy must be fail, overwrite, copy or skip")
			}
			mapping := map[string]string{}
			for _, m := range maps {
				from, to, ok := strings.Cut(m, "=")
				if !ok || from == "" || to == "" {
					return fmt.Errorf("--map takes old=new, got %q", m)
				}
				mapping[from] = to
			}
			docs, err := gitops.ParseFiles(path)
			if es, ok := gitops.AsErrors(err); ok {
				printPlan(cmd.OutOrStdout(), &applied{Plan: gitops.Plan{Errors: es}}, dryRun)
				return errPlanBlocked
			}
			if err != nil {
				return err
			}
			getenv := lookupEnv(env)
			var res *applied
			if r.server != "" {
				res, err = applyRemote(cmd.Context(), env, &r, docs, getenv, gitops.Policy(policy), mapping, dryRun)
			} else {
				err = local(cmd, env, func(ctx context.Context, a *app.App, p *auth.Principal) error {
					plan, err := a.Planner().Plan(ctx, docs, gitops.Options{
						Policy: gitops.Policy(policy), Map: mapping,
						Env: func(name string) (string, bool) { v := getenv(name); return v, v != "" },
					})
					if err != nil {
						return err
					}
					res = &applied{}
					if !plan.Blocked() {
						err := a.Planner().Apply(ctx, p, plan, auth.RequestMeta{IP: "cli"}, dryRun)
						if es, ok := gitops.AsErrors(err); ok {
							plan.Errors = append(plan.Errors, es...)
						} else if err != nil {
							return err
						}
						res.Applied = !dryRun && len(plan.Errors) == 0
					}
					res.Plan = *plan
					return nil
				})
			}
			if err != nil {
				return err
			}
			printPlan(cmd.OutOrStdout(), res, dryRun)
			if res.Blocked() || len(res.Errors) > 0 {
				return errPlanBlocked
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&path, "file", "f", "", "a YAML file, or a directory of .yaml and .yml files")
	_ = cmd.MarkFlagRequired("file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the plan and check it, changing nothing")
	cmd.Flags().StringVar(&policy, "policy", string(gitops.PolicyFail), "for resources that exist and differ: fail, overwrite, copy or skip")
	cmd.Flags().StringArrayVar(&maps, "map", nil, "old=new: use an existing connection or channel for one the documents reference (repeatable)")
	r.flags(cmd)
	return cmd
}

// applied is a plan and whether it was applied; the API's import result has the same shape.
type applied struct {
	gitops.Plan
	Applied bool `json:"applied"`
}

// applyRemote sends the documents to a server. Secrets come from this process's environment;
// positions in the server's answer are mapped back to the files.
func applyRemote(ctx context.Context, env Env, r *remote, docs []*gitops.Document, getenv func(string) string, policy gitops.Policy, mapping map[string]string, dryRun bool) (*applied, error) {
	var text bytes.Buffer
	type span struct {
		file        string
		first, last int
	}
	var spans []span
	files := map[string]bool{}
	for _, d := range docs {
		files[d.Pos.File] = true
	}
	line := 1
	for _, f := range slices.Sorted(maps.Keys(files)) {
		data, err := os.ReadFile(f) //nolint:gosec // the operator names the files
		if err != nil {
			return nil, err
		}
		if text.Len() > 0 {
			text.WriteString("---\n")
			line++
		}
		n := bytes.Count(data, []byte("\n"))
		if len(data) > 0 && data[len(data)-1] != '\n' {
			data = append(data, '\n')
			n++
		}
		spans = append(spans, span{f, line, line + n - 1})
		text.Write(data)
		line += n
	}
	secrets := map[string]string{}
	for _, d := range docs {
		var s map[string]any
		switch {
		case d.Connection != nil:
			s = d.Connection.Secrets
		case d.Channel != nil:
			s = d.Channel.Secrets
		}
		for _, v := range s {
			if name, ok := gitops.EnvName(v); ok && getenv(name) != "" {
				secrets[name] = getenv(name)
			}
		}
	}
	body := map[string]any{"yaml": text.String(), "dry_run": dryRun, "policy": policy, "map": mapping, "secrets": secrets}
	out, err := r.post(ctx, env, "/api/v1/import", body)
	if err != nil {
		return nil, err
	}
	res := &applied{}
	if err := json.Unmarshal(out, res); err != nil {
		return nil, fmt.Errorf("read the server's plan: %w", err)
	}
	fix := func(p *gitops.Pos) {
		for _, s := range spans {
			if p.Line >= s.first && p.Line <= s.last {
				p.File, p.Line = s.file, p.Line-s.first+1
				return
			}
		}
	}
	for i := range res.Items {
		fix(&res.Items[i].Pos)
	}
	for i := range res.Missing {
		fix(&res.Missing[i].Pos)
	}
	for i := range res.Errors {
		fix(&res.Errors[i].Pos)
	}
	return res, nil
}

// printPlan writes the plan like Terraform: one line per resource, the changes under updates, then
// what blocks it and a summary.
func printPlan(w io.Writer, res *applied, dryRun bool) {
	p := &res.Plan
	counts := map[gitops.Action]int{}
	marks := map[gitops.Action]string{
		gitops.ActionCreate: "+", gitops.ActionUpdate: "~", gitops.ActionUnchanged: "=", gitops.ActionConflict: "!", gitops.ActionSkip: "-",
	}
	for _, it := range p.Items {
		counts[it.Action]++
		name := it.Key()
		if it.Target != it.Name {
			name += " as " + it.Target
		}
		_, _ = fmt.Fprintf(w, "%s %-9s %s\n", marks[it.Action], it.Action, name)
		if it.Action == gitops.ActionUpdate || it.Action == gitops.ActionConflict {
			for _, c := range it.Changes {
				if c.Secret {
					_, _ = fmt.Fprintf(w, "      %s: (secret changes)\n", c.Field)
				} else {
					_, _ = fmt.Fprintf(w, "      %s: %s -> %s\n", c.Field, show(c.From), show(c.To))
				}
			}
		}
		if it.Action == gitops.ActionConflict {
			_, _ = fmt.Fprintln(w, "      exists and differs: choose --policy overwrite, copy or skip")
		}
	}
	for _, m := range p.Missing {
		_, _ = fmt.Fprintf(w, "missing: %s %q, referenced at %s (use --map %s=<existing>)\n", m.Kind, m.Name, m.Pos, m.Name)
	}
	for _, s := range p.Secrets {
		if s.Required && !s.Provided {
			_, _ = fmt.Fprintf(w, "secret:  set %s for %s %s (%s)\n", s.Env, s.Kind, s.Name, s.Field)
		}
	}
	for _, e := range p.Errors {
		_, _ = fmt.Fprintf(w, "error:   %s\n", e.Error())
	}
	verb := "Plan"
	switch {
	case res.Applied:
		verb = "Applied"
	case dryRun:
		verb = "Plan (dry run, nothing changed)"
	}
	_, _ = fmt.Fprintf(w, "%s: %d to create, %d to update, %d unchanged, %d skipped, %d conflicts.\n",
		verb, counts[gitops.ActionCreate], counts[gitops.ActionUpdate], counts[gitops.ActionUnchanged], counts[gitops.ActionSkip], counts[gitops.ActionConflict])
}

func show(v any) string {
	if v == nil {
		return "(none)"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	if s := string(b); len(s) <= 120 {
		return s
	}
	return string(b[:117]) + "..."
}
