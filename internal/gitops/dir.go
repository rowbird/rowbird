package gitops

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/store"
)

// DirResult is what applying the GitOps directory did.
type DirResult struct {
	Plan *Plan
	// Orphaned are reports whose documents left the directory in this apply (now paused).
	Orphaned []store.Report
	// Detached are resources an admin detached, which the directory no longer changes.
	Detached []string
}

// ApplyDir applies the configuration directory (ROWBIRD_CONFIG_DIR): documents win over the
// workspace (overwrite), except for resources an admin detached, and everything applied is marked
// as managed by GitOps. GitOps reports whose documents are gone are paused as orphans; nothing is
// deleted.
func (pl *Planner) ApplyDir(ctx context.Context, p *auth.Principal, dir string, env func(string) (string, bool), meta auth.RequestMeta) (*DirResult, error) {
	docs, err := ParseFiles(dir)
	if err != nil {
		return nil, err
	}
	res := &DirResult{}
	managed, err := pl.managed(ctx)
	if err != nil {
		return nil, err
	}
	skip := map[string]Policy{}
	for key, by := range managed {
		if by == store.ManagedByDetached {
			skip[key] = PolicySkip
			res.Detached = append(res.Detached, key)
		}
	}
	slices.Sort(res.Detached)
	plan, err := pl.Plan(ctx, docs, Options{Policy: PolicyOverwrite, Items: skip, Env: env})
	if err != nil {
		return nil, err
	}
	res.Plan = plan
	if plan.Blocked() {
		return res, ErrBlocked
	}
	inDir := map[string]bool{}
	for _, it := range plan.Items {
		inDir[it.Kind+"/"+it.Target] = true
	}
	ctx = store.WithManagedWrites(ctx)
	err = pl.apply2(ctx, p, plan, meta, false, func(ctx context.Context) error {
		for _, it := range plan.Items {
			if it.Action == ActionSkip || managed[it.Key()] == store.ManagedByDetached {
				continue
			}
			id, err := pl.idOf(ctx, it.Kind, it.Target)
			if err != nil {
				return err
			}
			if err := pl.store.SetManagedBy(ctx, strings.ToLower(it.Kind), id, store.ManagedByGitOps); err != nil {
				return err
			}
			if it.Kind == KindReport {
				if err := pl.returned(ctx, id, plan.resolved[it.Key()]); err != nil {
					return err
				}
			}
		}
		reports, err := pl.store.Reports().List(ctx)
		if err != nil {
			return err
		}
		for _, rp := range reports {
			if rp.ManagedBy != store.ManagedByGitOps || rp.GitOpsOrphan || inDir[KindReport+"/"+rp.Slug] {
				continue
			}
			if _, err := pl.reports.PauseWithReason(ctx, rp.ID, store.PausedGitOpsOrphan); err != nil {
				return err
			}
			if err := pl.store.Reports().SetOrphan(ctx, rp.ID, true); err != nil {
				return err
			}
			res.Orphaned = append(res.Orphaned, rp)
		}
		return nil
	})
	return res, err
}

// returned clears the orphan mark of a report back in the directory, resuming it unless its
// document says otherwise.
func (pl *Planner) returned(ctx context.Context, id uuid.UUID, r *resolved) error {
	rp, err := pl.store.Reports().Get(ctx, id)
	if err != nil || !rp.GitOpsOrphan {
		return err
	}
	if err := pl.store.Reports().SetOrphan(ctx, id, false); err != nil {
		return err
	}
	if r != nil && r.doc.Report.Enabled == nil && !rp.Enabled && rp.PausedReason != nil && *rp.PausedReason == store.PausedGitOpsOrphan {
		_, err = pl.reports.Resume(ctx, id)
	}
	return err
}

// managed maps the keys of resources with a manager to it.
func (pl *Planner) managed(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	conns, err := pl.store.Connections().List(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range conns {
		if c.ManagedBy != "" {
			out[KindConnection+"/"+c.Name] = c.ManagedBy
		}
	}
	chans, err := pl.store.Channels().List(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range chans {
		if c.ManagedBy != "" {
			out[KindChannel+"/"+c.Name] = c.ManagedBy
		}
	}
	qs, err := pl.store.Queries().List(ctx)
	if err != nil {
		return nil, err
	}
	for _, q := range qs {
		if q.ManagedBy != "" {
			out[KindQuery+"/"+q.Slug] = q.ManagedBy
		}
	}
	rps, err := pl.store.Reports().List(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rps {
		if r.ManagedBy != "" {
			out[KindReport+"/"+r.Slug] = r.ManagedBy
		}
	}
	return out, nil
}

func (pl *Planner) idOf(ctx context.Context, kind, name string) (uuid.UUID, error) {
	switch kind {
	case KindConnection:
		c, err := pl.store.Connections().GetByName(ctx, name)
		if err != nil {
			return uuid.Nil, err
		}
		return c.ID, nil
	case KindChannel:
		c, err := pl.store.Channels().GetByName(ctx, name)
		if err != nil {
			return uuid.Nil, err
		}
		return c.ID, nil
	case KindQuery:
		q, err := pl.store.Queries().GetBySlug(ctx, name)
		if err != nil {
			return uuid.Nil, err
		}
		return q.ID, nil
	case KindReport:
		r, err := pl.store.Reports().GetBySlug(ctx, name)
		if err != nil {
			return uuid.Nil, err
		}
		return r.ID, nil
	}
	return uuid.Nil, errors.New("gitops: unknown kind " + kind)
}
