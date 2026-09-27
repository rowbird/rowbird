package gitops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/condition"
	"github.com/rowbird/rowbird/internal/connections"
	"github.com/rowbird/rowbird/internal/params"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/reports"
)

// ErrBlocked is a plan with errors, missing references or secrets, or conflicts left to fail.
var ErrBlocked = errors.New("gitops: the plan cannot be applied")

var errDryRun = errors.New("gitops: dry run")

// Apply runs the plan in one transaction: everything is applied or nothing is. With dryRun the
// same changes run and are rolled back, so a dry run finds every error a real apply would.
// Connections are checked after the transaction commits.
func (pl *Planner) Apply(ctx context.Context, p *auth.Principal, plan *Plan, meta auth.RequestMeta, dryRun bool) error {
	return pl.apply2(ctx, p, plan, meta, dryRun, nil)
}

// apply2 is Apply with within, which runs in the same transaction after the documents.
func (pl *Planner) apply2(ctx context.Context, p *auth.Principal, plan *Plan, meta auth.RequestMeta, dryRun bool, within func(context.Context) error) error {
	if plan.Blocked() {
		return ErrBlocked
	}
	var recheck []uuid.UUID
	var errs Errors
	err := pl.store.RunInTx(connections.DeferChecks(ctx), func(ctx context.Context) error {
		for _, it := range plan.Items {
			if it.Action != ActionCreate && it.Action != ActionUpdate {
				continue
			}
			r := plan.resolved[it.Key()]
			id, err := pl.apply(ctx, p, it, r, meta)
			if err != nil {
				for _, e := range fieldErrors(it.Pos, "", err) {
					e.Message = it.Key() + ": " + e.Message
					errs = append(errs, e)
				}
				return errs
			}
			if it.Kind == KindConnection {
				recheck = append(recheck, id)
			}
		}
		if within != nil {
			if err := within(ctx); err != nil {
				return err
			}
		}
		if dryRun {
			return errDryRun
		}
		return nil
	})
	switch {
	case errors.Is(err, errDryRun):
		return nil
	case err != nil:
		return err
	}
	for _, id := range recheck {
		_ = pl.conns.Recheck(ctx, id)
	}
	return nil
}

func (pl *Planner) apply(ctx context.Context, p *auth.Principal, it Item, r *resolved, meta auth.RequestMeta) (uuid.UUID, error) {
	d := r.doc
	create := it.Action == ActionCreate
	switch d.Kind {
	case KindConnection:
		cfg := merged(r.config, r.secrets)
		o := d.Connection.Options
		if o == nil {
			o = &ConnectionOptions{}
		}
		if create {
			v, err := pl.conns.Create(ctx, p, connections.Input{
				Name: it.Target, Driver: d.Connection.Driver, Config: cfg, QueryTimeoutSeconds: o.QueryTimeoutSeconds,
				MaxRows: o.MaxRows, AllowMultiStatement: o.AllowMultiStatement, AIExcludedTables: o.AIExcludedTables,
			}, meta)
			if err != nil {
				return uuid.Nil, err
			}
			return v.ID, nil
		}
		c, err := pl.store.Connections().GetByName(ctx, it.Target)
		if err != nil {
			return uuid.Nil, err
		}
		patch := connections.Patch{Version: c.Version, Config: cfg, QueryTimeoutSeconds: o.QueryTimeoutSeconds, MaxRows: o.MaxRows, AllowMultiStatement: o.AllowMultiStatement}
		if o.AIExcludedTables != nil {
			patch.AIExcludedTables = &o.AIExcludedTables
		}
		_, err = pl.conns.Update(ctx, p, c.ID, patch, meta)
		return c.ID, err

	case KindChannel:
		cfg := merged(r.config, r.secrets)
		if create {
			v, err := pl.chans.Create(ctx, p, channels.Input{Name: it.Target, Type: d.Channel.Type, Config: cfg, IsSystemMailer: d.Channel.SystemMailer}, meta)
			if err != nil {
				return uuid.Nil, err
			}
			return v.ID, nil
		}
		c, err := pl.store.Channels().GetByName(ctx, it.Target)
		if err != nil {
			return uuid.Nil, err
		}
		mailer := d.Channel.SystemMailer
		_, err = pl.chans.Update(ctx, p, c.ID, channels.Patch{Version: c.Version, Config: cfg, IsSystemMailer: &mailer}, meta)
		return c.ID, err

	case KindQuery:
		conn, err := pl.store.Connections().GetByName(ctx, r.connection)
		if err != nil {
			return uuid.Nil, fmt.Errorf("connection %q: %w", r.connection, err)
		}
		defs := definitions(d.Query.Params)
		sql := strings.TrimSpace(d.Query.SQL)
		if create {
			v, err := pl.queries.Create(ctx, p, queries.Input{
				Title: orName(d.Metadata.Title, it.Target), Slug: it.Target, Description: d.Metadata.Description,
				ConnectionID: conn.ID, SQL: sql, Params: defs, Note: "config as code",
			})
			if err != nil {
				return uuid.Nil, err
			}
			return v.Query.ID, nil
		}
		q, err := pl.store.Queries().GetBySlug(ctx, it.Target)
		if err != nil {
			return uuid.Nil, err
		}
		patch := queries.Patch{Version: q.Version, ConnectionID: &conn.ID, SQL: &sql, Params: &defs, Note: "config as code"}
		if d.Metadata.Title != "" {
			patch.Title = &d.Metadata.Title
		}
		if d.Metadata.Description != "" {
			patch.Description = &d.Metadata.Description
		}
		_, err = pl.queries.Update(ctx, p, q.ID, patch)
		return q.ID, err

	case KindReport:
		return pl.applyReport(ctx, p, it, r)
	}
	return uuid.Nil, nil
}

func (pl *Planner) applyReport(ctx context.Context, p *auth.Principal, it Item, r *resolved) (uuid.UUID, error) {
	d := r.doc
	spec := d.Report
	q, err := pl.store.Queries().GetBySlug(ctx, r.query)
	if err != nil {
		return uuid.Nil, fmt.Errorf("query %q: %w", r.query, err)
	}
	cond := condition.Always()
	if r.cond != nil {
		cond = *r.cond
	}
	overrides := spec.Params
	if overrides == nil {
		overrides = map[string]string{}
	}
	run := spec.Run
	if run == nil {
		run = &RunPolicy{}
	}
	var rid uuid.UUID
	replaceDeliveries := true
	if it.Action == ActionCreate {
		in := reports.Input{
			Title: orName(d.Metadata.Title, it.Target), Slug: it.Target, Description: d.Metadata.Description, QueryID: q.ID,
			Enabled: spec.Enabled, Cron: spec.Schedule.Cron, Timezone: spec.Schedule.Timezone, Condition: cond, ParamOverrides: overrides,
			MaxRows: run.MaxRows, RetryMax: run.RetryMax, RetryBackoffSeconds: run.RetryBackoffSeconds,
			AutoPauseAfter: run.AutoPauseAfter, NotifyOwnerOnFailure: run.NotifyOwner,
		}
		if run.Misfire != nil {
			in.MisfirePolicy = *run.Misfire
		}
		if run.Overlap != nil {
			in.OverlapPolicy = *run.Overlap
		}
		v, err := pl.reports.Create(ctx, p, in)
		if err != nil {
			return uuid.Nil, err
		}
		rid = v.Report.ID
	} else {
		rp, err := pl.store.Reports().GetBySlug(ctx, it.Target)
		if err != nil {
			return uuid.Nil, err
		}
		rid = rp.ID
		patch := reports.Patch{
			Version: rp.Version, QueryID: &q.ID, Cron: &spec.Schedule.Cron, Condition: &cond, ParamOverrides: &overrides,
			MaxRows: run.MaxRows, RetryMax: run.RetryMax, RetryBackoffSeconds: run.RetryBackoffSeconds, MisfirePolicy: run.Misfire,
			OverlapPolicy: run.Overlap, AutoPauseAfter: run.AutoPauseAfter, NotifyOwnerOnFailure: run.NotifyOwner,
		}
		if d.Metadata.Title != "" {
			patch.Title = &d.Metadata.Title
		}
		if d.Metadata.Description != "" {
			patch.Description = &d.Metadata.Description
		}
		if spec.Schedule.Timezone != "" {
			patch.Timezone = &spec.Schedule.Timezone
		}
		if _, err := pl.reports.Update(ctx, rp.ID, patch); err != nil {
			return uuid.Nil, err
		}
		if spec.Enabled != nil && *spec.Enabled != rp.Enabled {
			if *spec.Enabled {
				_, err = pl.reports.Resume(ctx, rp.ID)
			} else {
				_, err = pl.reports.Pause(ctx, rp.ID)
			}
			if err != nil {
				return uuid.Nil, err
			}
		}
		// Deliveries keep their ids (and the resends of their runs) unless they changed.
		replaceDeliveries = hasChange(it.Changes, "spec.deliveries")
		if replaceDeliveries {
			existing, err := pl.store.Deliveries().ListByReport(ctx, rp.ID)
			if err != nil {
				return uuid.Nil, err
			}
			for _, e := range existing {
				if err := pl.reports.DeleteDelivery(ctx, rp.ID, e.ID); err != nil {
					return uuid.Nil, err
				}
			}
		}
	}
	if !replaceDeliveries {
		return rid, nil
	}
	for i, dl := range spec.Deliveries {
		ch, err := pl.store.Channels().GetByName(ctx, r.channels[i])
		if err != nil {
			return uuid.Nil, fmt.Errorf("channel %q: %w", r.channels[i], err)
		}
		n := normalizedDelivery(dl, r.channels[i], r.options[i])
		expires, _ := ParseDuration(n.Link.ExpiresIn)
		if _, err := pl.reports.CreateDelivery(ctx, rid, reports.DeliveryInput{
			ChannelID: ch.ID, Enabled: n.Enabled, Mode: n.Mode, Formats: n.Formats, InlineRowLimit: n.Inline.Rows,
			IncludeInlineWithFiles: n.Inline.WithFiles, LinkExpiresSeconds: &expires, LinkRequireLogin: n.Link.RequireLogin,
			Options: r.options[i],
		}); err != nil {
			return uuid.Nil, fmt.Errorf("deliveries[%d]: %w", i, err)
		}
	}
	return rid, nil
}

func hasChange(changes []Change, field string) bool {
	for _, c := range changes {
		if c.Field == field {
			return true
		}
	}
	return false
}

func merged(config, secrets map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range config {
		out[k] = v
	}
	for k, v := range secrets {
		out[k] = v
	}
	return out
}

func definitions(ps []Param) []params.Definition {
	out := make([]params.Definition, len(ps))
	for i, p := range ps {
		out[i] = params.Definition{Name: p.Name, Type: params.Type(p.Type), Default: p.Default}
	}
	return out
}

func orName(title, name string) string {
	if strings.TrimSpace(title) == "" {
		return name
	}
	return title
}
