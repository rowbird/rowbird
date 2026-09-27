package reports

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/store"
)

// RunView is a run with the names the UI shows next to it.
type RunView struct {
	Run             store.Run
	ReportTitle     string
	TriggeredByName string
	// Version is the query version that ran, when the run got that far.
	Version *store.QueryVersion
	// Attempts are the run's deliveries (only filled in by GetRun).
	Attempts []AttemptView
	// Files are the run's stored files (only filled in by GetRun).
	Files []store.Artifact
}

// AttemptView is a delivery attempt with its channel.
type AttemptView struct {
	Attempt     store.DeliveryAttempt
	ChannelName string
	ChannelType string
}

// Resendable reports whether the attempt can be sent again.
func (a AttemptView) Resendable() bool {
	return a.Attempt.Status == store.AttemptFailed && a.Attempt.DeliveryID != nil
}

// ListRuns returns a page of runs, newest first.
func (s *Service) ListRuns(ctx context.Context, f store.RunFilter, page store.PageRequest) (store.Page[RunView], error) {
	runs, err := s.store.Runs().List(ctx, f, page)
	if err != nil {
		return store.Page[RunView]{}, err
	}
	rps, err := s.store.Reports().List(ctx)
	if err != nil {
		return store.Page[RunView]{}, err
	}
	titles := make(map[uuid.UUID]string, len(rps))
	for _, rp := range rps {
		titles[rp.ID] = rp.Title
	}
	var userIDs []uuid.UUID
	for _, r := range runs.Items {
		if r.TriggeredBy != nil {
			userIDs = append(userIDs, *r.TriggeredBy)
		}
	}
	names := s.userNames(ctx, userIDs)
	out := store.Page[RunView]{Items: make([]RunView, len(runs.Items)), NextCursor: runs.NextCursor}
	for i, r := range runs.Items {
		out.Items[i] = RunView{Run: r, ReportTitle: titles[r.ReportID]}
		if r.TriggeredBy != nil {
			out.Items[i].TriggeredByName = names[*r.TriggeredBy]
		}
	}
	return out, nil
}

// GetRun returns one run with the query version it ran.
func (s *Service) GetRun(ctx context.Context, id uuid.UUID) (*RunView, error) {
	r, err := s.store.Runs().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	v := &RunView{Run: *r}
	if rp, err := s.store.Reports().Get(ctx, r.ReportID); err == nil {
		v.ReportTitle = rp.Title
	}
	if r.TriggeredBy != nil {
		v.TriggeredByName = s.userNames(ctx, []uuid.UUID{*r.TriggeredBy})[*r.TriggeredBy]
	}
	if r.QueryVersionID != nil {
		if qv, err := s.store.Queries().VersionByID(ctx, *r.QueryVersionID); err == nil {
			v.Version = qv
		}
	}
	if v.Files, err = s.RunFiles(ctx, id); err != nil {
		return nil, err
	}
	atts, err := s.store.Attempts().ListByRun(ctx, id)
	if err != nil {
		return nil, err
	}
	v.Attempts = make([]AttemptView, 0, len(atts))
	for _, a := range atts {
		v.Attempts = append(v.Attempts, s.attemptView(ctx, a))
	}
	return v, nil
}

func (s *Service) attemptView(ctx context.Context, a store.DeliveryAttempt) AttemptView {
	v := AttemptView{Attempt: a}
	if a.ChannelID != nil {
		if ch, err := s.store.Channels().Get(ctx, *a.ChannelID); err == nil {
			v.ChannelName, v.ChannelType = ch.Name, ch.Type
		}
	}
	return v
}

// RetryAttempt sends a failed attempt of a run again.
func (s *Service) RetryAttempt(ctx context.Context, runID, attemptID uuid.UUID) (*AttemptView, error) {
	a, err := s.store.Attempts().Get(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	if a.RunID != runID {
		return nil, store.ErrNotFound
	}
	if s.deliverer == nil {
		return nil, errors.New("reports: no delivery engine")
	}
	if a, err = s.deliverer.Resend(ctx, attemptID); err != nil {
		return nil, err
	}
	v := s.attemptView(ctx, *a)
	return &v, nil
}

// RetryFailed queues the failed attempts of a channel since the given time (24 hours ago when
// zero) for a resend in the background, and returns how many there are.
func (s *Service) RetryFailed(ctx context.Context, channelID uuid.UUID, since time.Time) (int, error) {
	if _, err := s.store.Channels().Get(ctx, channelID); err != nil {
		return 0, err
	}
	if s.deliverer == nil {
		return 0, errors.New("reports: no delivery engine")
	}
	if since.IsZero() {
		since = s.clock().Add(-24 * time.Hour)
	}
	ids, err := s.deliverer.FailedAttempts(ctx, channelID, since)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	bg := context.WithoutCancel(ctx)
	s.background(func() {
		for _, id := range ids {
			if _, err := s.deliverer.Resend(bg, id); err != nil {
				s.logger.WarnContext(bg, "bulk resend skipped an attempt", "attempt_id", id, "error", err)
			}
		}
	})
	return len(ids), nil
}

// CancelRun cancels a pending run at once, or asks the worker running it to stop.
func (s *Service) CancelRun(ctx context.Context, id uuid.UUID) (*RunView, error) {
	r, err := s.store.Runs().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	at := s.clock()
	var ok bool
	switch r.Status {
	case store.RunPending:
		ok, err = s.store.Runs().CancelPending(ctx, id, at, CodeCancelled)
	case store.RunRunning:
		if ok, err = s.store.Runs().RequestCancel(ctx, id, at); ok {
			s.cancel(id)
		}
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrRunNotActive
	}
	s.logger.InfoContext(ctx, "run cancel requested", "run_id", id, "report_id", r.ReportID)
	if r.Status == store.RunPending {
		r.Status = store.RunCancelled
		s.notifier.RunUpdated(ctx, r)
	}
	return s.GetRun(ctx, id)
}

func (s *Service) userNames(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	if len(ids) == 0 {
		return out
	}
	users, err := s.store.Users().GetMany(ctx, ids)
	if err != nil {
		return out
	}
	for _, u := range users {
		out[u.ID] = u.Name
	}
	return out
}
