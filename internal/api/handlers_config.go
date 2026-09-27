package api

import (
	"bytes"
	"context"
	"errors"
	"mime"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/gitops"
)

type configHandlers struct {
	planner  *gitops.Planner
	exporter *gitops.Exporter
}

func (h *configHandlers) ExportConfig(ctx context.Context, req gen.ExportConfigRequestObject) (gen.ExportConfigResponseObject, error) {
	sel := gitops.Selection{}
	if b := req.Body; b != nil {
		if b.Reports != nil {
			sel.Reports = *b.Reports
		}
		if b.Queries != nil {
			sel.Queries = *b.Queries
		}
		sel.IncludeConnections = b.IncludeConnections != nil && *b.IncludeConnections
		sel.IncludeChannels = b.IncludeChannels != nil && *b.IncludeChannels
	}
	docs, err := h.exporter.Export(ctx, sel)
	var unknown *gitops.ErrUnknownSelection
	if errors.As(err, &unknown) {
		field := "reports"
		if unknown.Kind == gitops.KindQuery {
			field = "queries"
		}
		return nil, apperr.Invalid(apperr.Field(field, "validation.not_found"))
	}
	if err != nil {
		return nil, err
	}
	out, err := gitops.Encode(docs)
	if err != nil {
		return nil, err
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": "rowbird-export.yaml"})
	return gen.ExportConfig200ApplicationyamlResponse{
		Body: bytes.NewReader(out), ContentLength: int64(len(out)), Headers: gen.ExportConfig200ResponseHeaders{ContentDisposition: &disposition},
	}, nil
}

func (h *configHandlers) ImportConfig(ctx context.Context, req gen.ImportConfigRequestObject) (gen.ImportConfigResponseObject, error) {
	b := req.Body
	opts := gitops.Options{Items: map[string]gitops.Policy{}, Map: map[string]string{}, Secrets: map[string]string{}}
	var fe []apperr.FieldError
	if b.Policy != nil {
		opts.Policy = gitops.Policy(*b.Policy)
	}
	if b.Items != nil {
		for k, v := range *b.Items {
			opts.Items[k] = gitops.Policy(v)
		}
	}
	if b.Map != nil {
		opts.Map = *b.Map
	}
	if b.Secrets != nil {
		opts.Secrets = *b.Secrets
	}
	if opts.Policy != "" && !gitops.ValidPolicy(opts.Policy) {
		fe = append(fe, apperr.Field("policy", "validation.invalid_value"))
	}
	if len(fe) > 0 {
		return nil, apperr.Invalid(fe...)
	}
	out := gen.ImportConfig200JSONResponse{Items: []gen.ImportItem{}, Missing: []gen.ImportMissing{}, Secrets: []gen.ImportSecret{}, Errors: []gen.ImportError{}}
	docs, err := gitops.ParseSet("import.yaml", []byte(b.Yaml))
	if es, ok := gitops.AsErrors(err); ok {
		out.Errors = importErrors(es)
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	plan, err := h.planner.Plan(ctx, docs, opts)
	if err != nil {
		return nil, err
	}
	dryRun := b.DryRun == nil || *b.DryRun
	if !plan.Blocked() {
		err := h.planner.Apply(ctx, PrincipalFrom(ctx), plan, requestMetaFrom(ctx), dryRun)
		if es, ok := gitops.AsErrors(err); ok {
			plan.Errors = append(plan.Errors, es...)
		} else if err != nil {
			return nil, err
		}
		out.Applied = !dryRun && len(plan.Errors) == 0
	}
	for _, it := range plan.Items {
		item := gen.ImportItem{
			Kind: gen.ImportItemKind(it.Kind), Name: it.Name, Pos: importPos(it.Pos), Action: gen.ImportItemAction(it.Action),
			Target: it.Target, Changes: make([]gen.ImportChange, len(it.Changes)),
		}
		if it.Policy != "" {
			p := gen.ImportPolicy(it.Policy)
			item.Policy = &p
		}
		for i, c := range it.Changes {
			ch := gen.ImportChange{Field: c.Field}
			if c.Secret {
				t := true
				ch.Secret = &t
			} else {
				from, to := c.From, c.To
				ch.From, ch.To = &from, &to
			}
			item.Changes[i] = ch
		}
		out.Items = append(out.Items, item)
	}
	for _, m := range plan.Missing {
		out.Missing = append(out.Missing, gen.ImportMissing{Kind: m.Kind, Name: m.Name, Pos: importPos(m.Pos)})
	}
	for _, s := range plan.Secrets {
		out.Secrets = append(out.Secrets, gen.ImportSecret{Env: s.Env, Kind: s.Kind, Name: s.Name, Field: s.Field, Required: s.Required, Provided: s.Provided})
	}
	out.Errors = importErrors(plan.Errors)
	return out, nil
}

func importPos(p gitops.Pos) gen.ImportPos {
	return gen.ImportPos{File: p.File, Line: p.Line, Column: p.Column}
}

func importErrors(es gitops.Errors) []gen.ImportError {
	out := make([]gen.ImportError, len(es))
	for i, e := range es {
		out[i] = gen.ImportError{Pos: importPos(e.Pos), Message: e.Message}
	}
	return out
}

func (h *configHandlers) DetachFromGitOps(ctx context.Context, req gen.DetachFromGitOpsRequestObject) (gen.DetachFromGitOpsResponseObject, error) {
	if err := h.planner.SetManaged(ctx, PrincipalFrom(ctx), string(req.Body.Kind), req.Body.Id, false, requestMetaFrom(ctx)); err != nil {
		return nil, err
	}
	return gen.DetachFromGitOps204Response{}, nil
}

func (h *configHandlers) AttachToGitOps(ctx context.Context, req gen.AttachToGitOpsRequestObject) (gen.AttachToGitOpsResponseObject, error) {
	if err := h.planner.SetManaged(ctx, PrincipalFrom(ctx), string(req.Body.Kind), req.Body.Id, true, requestMetaFrom(ctx)); err != nil {
		return nil, err
	}
	return gen.AttachToGitOps204Response{}, nil
}
