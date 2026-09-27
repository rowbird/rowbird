// Package links serves and manages shared links: downloads through a secret token, with expiry,
// an optional login requirement, revocation and a download log (docs/spec/07-security.md,
// "Shared links").
package links

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/delivery"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/store"
)

// PresignTTL is how long a redirect to the storage stays valid.
const PresignTTL = 5 * time.Minute

// Link statuses.
const (
	StatusActive  = "active"
	StatusExpired = "expired"
	StatusRevoked = "revoked"
)

// Errors of the public download.
var (
	ErrNotFound      = apperr.New(apperr.KindNotFound, "link.not_found")
	ErrGone          = apperr.New(apperr.KindGone, "link.expired")
	ErrLoginRequired = apperr.New(apperr.KindUnauthenticated, "link.login_required")
	ErrRevoked       = apperr.New(apperr.KindConflict, "link.revoked")
)

// Service serves links.
type Service struct {
	store   *store.Store
	storage plugin.Storage
	logger  *slog.Logger
	now     func() time.Time
}

// Options configure the service.
type Options struct {
	Logger *slog.Logger
	Now    func() time.Time
}

// NewService builds the service.
func NewService(st *store.Store, storage plugin.Storage, opts Options) *Service {
	s := &Service{store: st, storage: storage, logger: opts.Logger, now: opts.Now}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

func (s *Service) clock() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

// Download is what the public route sends: a redirect to the storage, or the file itself.
type Download struct {
	RedirectURL string
	Name        string
	ContentType string
	Size        int64
	Body        io.ReadCloser
}

// Open resolves a token for a download and records it. p is the caller's session, if any.
func (s *Service) Open(ctx context.Context, token string, p *auth.Principal, meta auth.RequestMeta) (*Download, error) {
	if !strings.HasPrefix(token, "rbl_") || len(token) > 100 {
		return nil, ErrNotFound
	}
	ref, err := s.store.System().LinkByTokenHash(ctx, delivery.HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ctx = ref.Context(ctx)
	l, err := s.store.Links().Get(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	if l.RevokedAt != nil || !l.ExpiresAt.After(s.clock()) {
		return nil, ErrGone
	}
	if l.RequireLogin && (p == nil || p.WorkspaceID != ref.WorkspaceID) {
		return nil, ErrLoginRequired
	}
	art, err := s.store.Artifacts().Get(ctx, l.ArtifactID)
	if err != nil || art.DeletedAt != nil {
		return nil, ErrGone
	}
	dl := &store.LinkDownload{LinkID: l.ID, IP: meta.IP, UserAgent: truncate(meta.UserAgent, 500)}
	if p != nil && p.WorkspaceID == ref.WorkspaceID {
		dl.UserID = &p.UserID
	}
	dl.CreatedAt = s.clock()
	if err := s.store.Links().RecordDownload(ctx, dl); err != nil {
		return nil, err
	}
	s.logger.InfoContext(ctx, "shared link downloaded", "link_id", l.ID, "run_id", l.RunID)
	om := plugin.ObjectMeta{ContentType: art.ContentType, FileName: art.FileName, Size: art.SizeBytes}
	if u, err := s.storage.PresignGet(ctx, art.StorageKey, PresignTTL, om); err == nil {
		return &Download{RedirectURL: u}, nil
	} else if !errors.Is(err, plugin.ErrPresignUnsupported) {
		return nil, err
	}
	body, got, err := s.storage.Get(ctx, art.StorageKey)
	if errors.Is(err, plugin.ErrObjectNotFound) {
		return nil, ErrGone
	}
	if err != nil {
		return nil, err
	}
	return &Download{Name: art.FileName, ContentType: art.ContentType, Size: got.Size, Body: body}, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// View is a link as the API shows it. The token itself cannot be shown again: only its hash is
// kept.
type View struct {
	Link          store.SharedLink
	Status        string
	FileName      string
	Format        string
	ReportID      uuid.UUID
	ReportTitle   string
	RevokedByName string
	Downloads     []store.LinkDownload
	// UserNames names the signed-in downloaders, by user id.
	UserNames map[uuid.UUID]string
}

// Filter narrows a listing.
type Filter struct {
	RunID      uuid.UUID
	ActiveOnly bool
}

// List returns a page of links, newest first.
func (s *Service) List(ctx context.Context, f Filter, page store.PageRequest) (store.Page[View], error) {
	lf := store.LinkFilter{RunID: f.RunID}
	if f.ActiveOnly {
		lf.ActiveAt = s.clock()
	}
	links, err := s.store.Links().List(ctx, lf, page)
	if err != nil {
		return store.Page[View]{}, err
	}
	out := store.Page[View]{Items: make([]View, 0, len(links.Items)), NextCursor: links.NextCursor}
	for i := range links.Items {
		v, err := s.view(ctx, &links.Items[i])
		if err != nil {
			return store.Page[View]{}, err
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}

// Get returns a link with its latest downloads.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*View, error) {
	l, err := s.store.Links().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	v, err := s.view(ctx, l)
	if err != nil {
		return nil, err
	}
	if v.Downloads, err = s.store.Links().Downloads(ctx, id, 100); err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for _, d := range v.Downloads {
		if d.UserID != nil {
			ids = append(ids, *d.UserID)
		}
	}
	v.UserNames = map[uuid.UUID]string{}
	if len(ids) > 0 {
		users, err := s.store.Users().GetMany(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			v.UserNames[u.ID] = u.Name
		}
	}
	return &v, nil
}

// Revoke ends a link at once.
func (s *Service) Revoke(ctx context.Context, p *auth.Principal, id uuid.UUID) (*View, error) {
	ok, err := s.store.Links().Revoke(ctx, id, p.UserID, s.clock())
	if err != nil {
		return nil, err
	}
	if !ok {
		if _, err := s.store.Links().Get(ctx, id); err != nil {
			return nil, err
		}
		return nil, ErrRevoked
	}
	s.logger.InfoContext(ctx, "shared link revoked", "link_id", id)
	return s.Get(ctx, id)
}

func (s *Service) view(ctx context.Context, l *store.SharedLink) (View, error) {
	v := View{Link: *l, Status: StatusActive}
	switch {
	case l.RevokedAt != nil:
		v.Status = StatusRevoked
	case !l.ExpiresAt.After(s.clock()):
		v.Status = StatusExpired
	}
	if a, err := s.store.Artifacts().Get(ctx, l.ArtifactID); err == nil {
		v.FileName, v.Format = a.FileName, a.Format
	}
	if run, err := s.store.Runs().Get(ctx, l.RunID); err == nil {
		v.ReportID = run.ReportID
		if rp, err := s.store.Reports().Get(ctx, run.ReportID); err == nil {
			v.ReportTitle = rp.Title
		}
	}
	if l.RevokedBy != nil {
		if u, err := s.store.Users().Get(ctx, *l.RevokedBy); err == nil {
			v.RevokedByName = u.Name
		}
	}
	return v, nil
}
