package gitops_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/apperr"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/condition"
	_ "github.com/rowbird/rowbird/internal/condition"
	_ "github.com/rowbird/rowbird/internal/destination/uptimekuma"
	_ "github.com/rowbird/rowbird/internal/destination/webhook"
	_ "github.com/rowbird/rowbird/internal/format/csv"
	_ "github.com/rowbird/rowbird/internal/format/xlsx"
	"github.com/rowbird/rowbird/internal/gitops"
	"github.com/rowbird/rowbird/internal/params"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/reports"
	"github.com/rowbird/rowbird/internal/testenv"
)

// workspace is a test environment with the services an export or an import needs.
type workspace struct {
	*testenv.Env
	channels *channels.Service
	reports  *reports.Service
	exporter *gitops.Exporter
}

func newWorkspace(t *testing.T) *workspace {
	t.Helper()
	return workspaceOn(testenv.New(t))
}

func workspaceOn(env *testenv.Env) *workspace {
	chs := channels.NewService(env.Store, env.Keyring, channels.Options{})
	rps := reports.NewService(env.Store, env.Queries, reports.Options{Now: env.Clock.Now, BaseURL: "https://rb.example.com"})
	return &workspace{Env: env, channels: chs, reports: rps, exporter: gitops.NewExporter(env.Store, env.Conns, chs)}
}

// seed creates a query with parameters, a webhook channel with a secret, and a report delivering
// to it with a condition and every policy set.
func (w *workspace) seed(t *testing.T) {
	t.Helper()
	def := "south"
	q := w.Query(t, "orders-by-region", "select region, sum(total) as total from orders where region = {{region}} group by region",
		params.Definition{Name: "region", Type: params.Text, Default: &def})
	title := "Orders by region"
	if _, err := w.Queries.Update(w.Ctx, w.Principal, q.Query.ID, queriesPatch(q.Query.Version, title)); err != nil {
		fatal(t, err)
	}
	ch, err := w.channels.Create(w.Ctx, w.Principal, channels.Input{Name: "erp-hook", Type: "webhook", Config: map[string]any{
		"url": "https://erp.example.com/hook", "hmac_secret": "hook-secret-value",
	}}, auth.RequestMeta{})
	if err != nil {
		fatal(t, err)
	}
	enabled, maxRows, retry, backoff, pause, notify := true, 500, 3, 60, 7, false
	r, err := w.reports.Create(w.Ctx, w.Principal, reports.Input{
		Title: "Daily orders", Slug: "daily-orders", Description: "Orders per region", QueryID: q.Query.ID, Enabled: &enabled,
		Cron: "0 7 * * 1-5", Timezone: "America/Sao_Paulo", ParamOverrides: map[string]string{"region": "north"},
		Condition: condition.Spec{Match: "any", Rules: []condition.Rule{{Type: "row_count", Params: map[string]any{"op": "gt", "value": int64(2)}}, {Type: "has_rows"}}},
		MaxRows:   &maxRows, RetryMax: &retry, RetryBackoffSeconds: &backoff, MisfirePolicy: "skip", OverlapPolicy: "queue",
		AutoPauseAfter: &pause, NotifyOwnerOnFailure: &notify,
	})
	if err != nil {
		fatal(t, err)
	}
	rows, withFiles, expires, login := 15, false, 3*86400, true
	if _, err := w.reports.CreateDelivery(w.Ctx, r.Report.ID, reports.DeliveryInput{
		ChannelID: ch.ID, Mode: "link", Formats: []string{"csv", "xlsx"}, InlineRowLimit: &rows, IncludeInlineWithFiles: &withFiles,
		LinkExpiresSeconds: &expires, LinkRequireLogin: &login, Options: map[string]any{"include_rows": 5},
	}); err != nil {
		fatal(t, err)
	}
}

func queriesPatch(version int64, title string) queries.Patch {
	desc := "Totals per region"
	return queries.Patch{Version: version, Title: &title, Description: &desc}
}

func fatal(t *testing.T, err error) {
	t.Helper()
	if ae, ok := apperr.As(err); ok {
		t.Fatalf("%s %+v", ae.Code, ae.Fields)
	}
	t.Fatal(fmt.Sprint(err))
}

func (w *workspace) planner() *gitops.Planner {
	return gitops.NewPlanner(w.Store, w.Conns, w.channels, w.Queries, w.reports)
}

func (w *workspace) export(t *testing.T) string {
	t.Helper()
	docs, err := w.exporter.Export(w.Ctx, gitops.Selection{IncludeConnections: true, IncludeChannels: true})
	if err != nil {
		fatal(t, err)
	}
	out, err := gitops.Encode(docs)
	if err != nil {
		fatal(t, err)
	}
	return strings.ReplaceAll(string(out), w.Dir, "/data/sqlite")
}

// plan parses text (with /data/sqlite as this workspace's SQLite directory) and plans it.
func (w *workspace) plan(t *testing.T, text string, opts gitops.Options) (*gitops.Plan, []*gitops.Document) {
	t.Helper()
	docs, err := gitops.ParseSet("in.yaml", []byte(strings.ReplaceAll(text, "/data/sqlite", w.Dir)))
	if err != nil {
		fatal(t, err)
	}
	plan, err := w.planner().Plan(w.Ctx, docs, opts)
	if err != nil {
		fatal(t, err)
	}
	return plan, docs
}

func actions(plan *gitops.Plan) string {
	var out []string
	for _, it := range plan.Items {
		s := it.Key() + "=" + string(it.Action)
		if it.Target != it.Name {
			s += "->" + it.Target
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

func errBlocked() error { return gitops.ErrBlocked }
