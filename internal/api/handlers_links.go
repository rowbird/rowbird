package api

import (
	"context"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/links"
)

type linkHandlers struct {
	svc *links.Service
}

func toLink(v *links.View) gen.SharedLink {
	l := v.Link
	out := gen.SharedLink{
		Id: l.ID, RunId: l.RunID, ReportId: v.ReportID, ReportTitle: v.ReportTitle, DeliveryId: l.DeliveryID,
		FileName: v.FileName, Format: v.Format, Status: gen.SharedLinkStatus(v.Status), ExpiresAt: l.ExpiresAt,
		RequireLogin: l.RequireLogin, RevokedAt: l.RevokedAt, DownloadCount: l.DownloadCount, LastDownloadAt: l.LastDownloadAt,
		CreatedAt: l.CreatedAt,
	}
	if v.RevokedByName != "" {
		out.RevokedByName = &v.RevokedByName
	}
	return out
}

func toLinkDetail(v *links.View) gen.SharedLinkDetail {
	s := toLink(v)
	out := gen.SharedLinkDetail{
		Id: s.Id, RunId: s.RunId, ReportId: s.ReportId, ReportTitle: s.ReportTitle, DeliveryId: s.DeliveryId, FileName: s.FileName,
		Format: s.Format, Status: gen.SharedLinkDetailStatus(s.Status), ExpiresAt: s.ExpiresAt, RequireLogin: s.RequireLogin,
		RevokedAt: s.RevokedAt, RevokedByName: s.RevokedByName, DownloadCount: s.DownloadCount, LastDownloadAt: s.LastDownloadAt,
		CreatedAt: s.CreatedAt, Downloads: make([]gen.LinkDownload, len(v.Downloads)),
	}
	for i, d := range v.Downloads {
		out.Downloads[i] = gen.LinkDownload{At: d.CreatedAt, Ip: d.IP, UserAgent: d.UserAgent, UserId: d.UserID}
		if d.UserID != nil {
			if name, ok := v.UserNames[*d.UserID]; ok {
				out.Downloads[i].UserName = &name
			}
		}
	}
	return out
}

func (h *linkHandlers) ListLinks(ctx context.Context, req gen.ListLinksRequestObject) (gen.ListLinksResponseObject, error) {
	f := links.Filter{}
	if req.Params.RunId != nil {
		f.RunID = *req.Params.RunId
	}
	if req.Params.Active != nil {
		f.ActiveOnly = *req.Params.Active
	}
	page, err := h.svc.List(ctx, f, pageRequest(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out := gen.ListLinks200JSONResponse{Items: make([]gen.SharedLink, len(page.Items)), NextCursor: nextCursor(page.NextCursor)}
	for i := range page.Items {
		out.Items[i] = toLink(&page.Items[i])
	}
	return out, nil
}

func (h *linkHandlers) GetLink(ctx context.Context, req gen.GetLinkRequestObject) (gen.GetLinkResponseObject, error) {
	v, err := h.svc.Get(ctx, req.LinkId)
	if err != nil {
		return nil, err
	}
	return gen.GetLink200JSONResponse(toLinkDetail(v)), nil
}

func (h *linkHandlers) RevokeLink(ctx context.Context, req gen.RevokeLinkRequestObject) (gen.RevokeLinkResponseObject, error) {
	v, err := h.svc.Revoke(ctx, PrincipalFrom(ctx), req.LinkId)
	if err != nil {
		return nil, err
	}
	return gen.RevokeLink200JSONResponse(toLinkDetail(v)), nil
}
