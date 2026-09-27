package reports

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/store"
)

// Dashboard sizes.
const (
	dashboardNextRuns = 5
	dashboardFailures = 5
)

// Dashboard is what the home page and the health banner show (docs/spec/06-ui.md, "Home").
type Dashboard struct {
	NextRuns        []store.Report
	RecentFailures  []RunView
	Rate7, Rate30   SuccessRate
	FailingChannels []store.Channel
	// FailingReports failed their latest counted run or were paused after failures.
	FailingReports []store.Report
	Onboarding     Onboarding
}

// Onboarding tracks the first-use checklist: connect a database, write a query, schedule a
// report, receive a first delivery.
type Onboarding struct {
	HasConnection, HasQuery, HasReport, HasDelivery bool
}

// SuccessRate counts the runs that count toward health: Succeeded are the successful ones (partial
// runs delivered only part and count as not succeeded), skipped runs are left out.
type SuccessRate struct {
	Succeeded int
	Total     int
}

// Dashboard builds the home page summary.
func (s *Service) Dashboard(ctx context.Context) (*Dashboard, error) {
	all, err := s.store.Reports().List(ctx)
	if err != nil {
		return nil, err
	}
	d := &Dashboard{}
	titles := map[uuid.UUID]string{}
	for _, rp := range all {
		titles[rp.ID] = rp.Title
		if rp.Enabled && rp.NextRunAt != nil {
			d.NextRuns = append(d.NextRuns, rp)
		}
		if rp.ConsecutiveFailures > 0 || (rp.PausedReason != nil && *rp.PausedReason == store.PausedAutoFailures) {
			d.FailingReports = append(d.FailingReports, rp)
		}
	}
	slices.SortFunc(d.NextRuns, func(a, b store.Report) int { return a.NextRunAt.Compare(*b.NextRunAt) })
	d.NextRuns = d.NextRuns[:min(len(d.NextRuns), dashboardNextRuns)]

	failures, err := s.store.Runs().List(ctx, store.RunFilter{Statuses: []string{store.RunFailed, store.RunPartial}}, store.PageRequest{Limit: dashboardFailures})
	if err != nil {
		return nil, err
	}
	for _, r := range failures.Items {
		d.RecentFailures = append(d.RecentFailures, RunView{Run: r, ReportTitle: titles[r.ReportID]})
	}

	now := s.clock()
	for _, w := range []struct {
		days int
		out  *SuccessRate
	}{{7, &d.Rate7}, {30, &d.Rate30}} {
		counts, err := s.store.Runs().CountOutcomes(ctx, now.Add(-time.Duration(w.days)*24*time.Hour))
		if err != nil {
			return nil, err
		}
		w.out.Succeeded = counts[store.RunSuccess]
		w.out.Total = counts[store.RunSuccess] + counts[store.RunPartial] + counts[store.RunFailed]
	}

	d.Onboarding.HasReport = len(all) > 0
	conns, err := s.store.Connections().List(ctx)
	if err != nil {
		return nil, err
	}
	d.Onboarding.HasConnection = len(conns) > 0
	queries, err := s.store.Queries().List(ctx)
	if err != nil {
		return nil, err
	}
	d.Onboarding.HasQuery = len(queries) > 0
	if d.Onboarding.HasDelivery, err = s.store.Attempts().AnySent(ctx); err != nil {
		return nil, err
	}

	channels, err := s.store.Channels().List(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range channels {
		if c.Status == store.ChannelFailing {
			c.SecretsEnc = nil
			d.FailingChannels = append(d.FailingChannels, c)
		}
	}
	return d, nil
}
