package app

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/gitops"
	"github.com/rowbird/rowbird/internal/store"
)

// setupPoll is how often a server started before setup looks for the workspace, to apply the
// configuration directory as soon as it exists.
var setupPoll = 2 * time.Second

// applyConfigDir applies ROWBIRD_CONFIG_DIR to the workspace at startup, or right after setup
// when the server starts before it (then in the background). Problems are logged and notified to
// admins; they never stop the server, and a blocked plan applies nothing.
func (a *App) applyConfigDir(ctx context.Context) {
	dir := a.cfg.ConfigDir
	if _, _, err := a.AdminContext(ctx); errors.Is(err, ErrNoWorkspace) {
		a.logger.InfoContext(ctx, "the configuration directory will be applied when setup is done", "dir", dir)
		go func() {
			t := time.NewTicker(setupPoll)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if _, _, err := a.AdminContext(ctx); !errors.Is(err, ErrNoWorkspace) {
						a.applyConfigDirNow(ctx)
						return
					}
				}
			}
		}()
		return
	}
	a.applyConfigDirNow(ctx)
}

func (a *App) applyConfigDirNow(ctx context.Context) {
	dir := a.cfg.ConfigDir
	ctx, p, err := a.AdminContext(ctx)
	if err != nil {
		a.logger.ErrorContext(ctx, "gitops: cannot act for the workspace", "error", err)
		return
	}
	res, err := a.planner.ApplyDir(ctx, p, dir, os.LookupEnv, auth.RequestMeta{IP: "gitops"})
	if err != nil {
		if res != nil && res.Plan != nil {
			err = planProblems(res.Plan, err)
		}
		a.logger.ErrorContext(ctx, "the configuration directory was not applied", "dir", dir, "error", err)
		a.notify.GitOpsApplied(ctx, err, nil)
		return
	}
	counts := map[gitops.Action]int{}
	for _, it := range res.Plan.Items {
		counts[it.Action]++
	}
	a.logger.InfoContext(ctx, "configuration directory applied", "dir", dir,
		"created", counts[gitops.ActionCreate], "updated", counts[gitops.ActionUpdate], "unchanged", counts[gitops.ActionUnchanged],
		"detached", len(res.Detached), "orphaned", len(res.Orphaned))
	for _, key := range res.Detached {
		a.logger.WarnContext(ctx, "gitops: resource detached by an admin is left as it is", "resource", key)
	}
	for _, rp := range res.Orphaned {
		a.logger.WarnContext(ctx, "gitops: report left the configuration directory and was paused", "report", rp.Slug)
	}
	a.notify.GitOpsApplied(ctx, nil, res.Orphaned)
}

// planProblems describes why a plan is blocked, for logs and the admins' notification.
func planProblems(plan *gitops.Plan, err error) error {
	var es gitops.Errors
	es = append(es, plan.Errors...)
	for _, m := range plan.Missing {
		es = append(es, gitops.Error{Pos: m.Pos, Message: m.Kind + " " + m.Name + " does not exist"})
	}
	for _, s := range plan.Secrets {
		if s.Required && !s.Provided {
			es = append(es, gitops.Error{Message: s.Kind + " " + s.Name + ": set " + s.Env + " for " + s.Field})
		}
	}
	for _, it := range plan.Items {
		if it.Action == gitops.ActionConflict {
			es = append(es, gitops.Error{Pos: it.Pos, Message: it.Key() + " conflicts"})
		}
	}
	if len(es) == 0 {
		return err
	}
	return es
}

// ErrNoWorkspace means setup has not created the workspace yet.
var ErrNoWorkspace = errors.New("setup has not been done yet: there is no workspace")

// AdminContext returns ctx bound to the workspace and an admin to act as, for work done by the
// server itself or by the local CLI (the configuration directory, rowbird apply).
func (a *App) AdminContext(ctx context.Context) (context.Context, *auth.Principal, error) {
	ws, err := a.store.Workspaces().GetBySlug(ctx, auth.DefaultWorkspaceSlug)
	if errors.Is(err, store.ErrNotFound) {
		return ctx, nil, ErrNoWorkspace
	}
	if err != nil {
		return ctx, nil, err
	}
	ctx = store.WithWorkspace(ctx, ws.ID)
	admins, err := a.store.Members().ActiveAdminIDs(ctx)
	if err != nil {
		return ctx, nil, err
	}
	if len(admins) == 0 {
		return ctx, nil, errors.New("the workspace has no active admin")
	}
	return ctx, &auth.Principal{UserID: admins[0], WorkspaceID: ws.ID, Role: store.RoleAdmin}, nil
}

// Planner imports configuration as code; Exporter exports it (the local CLI uses both).
func (a *App) Planner() *gitops.Planner { return a.planner }

// Exporter exports configuration as code.
func (a *App) Exporter() *gitops.Exporter { return a.exporter }

// Close releases the store, for commands that do not serve.
func (a *App) Close() error { return a.store.Close() }
