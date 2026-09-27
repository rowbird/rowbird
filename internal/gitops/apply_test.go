package gitops_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/gitops"
	"github.com/rowbird/rowbird/internal/store/storetest"
	"github.com/rowbird/rowbird/internal/testenv"
)

const hookSecretEnv = "ROWBIRD_CHANNEL_ERP_HOOK_HMAC_SECRET"

// TestRoundTrip is the phase's acceptance test: what one workspace exports, another imports
// without losing anything but the secrets, which the import supplies.
func TestRoundTrip(t *testing.T) {
	for dialect, newURL := range storetest.Dialects() {
		t.Run(string(dialect), func(t *testing.T) {
			roundTrip(t, workspaceOn(testenv.NewOn(t, newURL(t))), workspaceOn(testenv.NewOn(t, newURL(t))))
		})
	}
}

func roundTrip(t *testing.T, src, dst *workspace) {
	src.seed(t)
	text := src.export(t)

	plan, _ := dst.plan(t, text, gitops.Options{Policy: gitops.PolicyOverwrite})
	// The destination has its own "shop" connection (another directory) and lacks the rest.
	if got := actions(plan); got != "Connection/shop=unchanged Channel/erp-hook=create Query/orders-by-region=create Report/daily-orders=create" {
		t.Fatalf("plan %s %+v", got, plan.Errors)
	}
	if !plan.Blocked() || len(plan.Secrets) != 1 || plan.Secrets[0].Env != hookSecretEnv || !plan.Secrets[0].Required {
		t.Fatalf("the channel secret should be needed: %+v", plan.Secrets)
	}
	if err := dst.planner().Apply(dst.Ctx, dst.Principal, plan, auth.RequestMeta{}, false); !errors.Is(err, gitops.ErrBlocked) {
		t.Fatalf("blocked plan applied: %v", err)
	}

	env := func(name string) (string, bool) { return "hook-secret-value", name == hookSecretEnv }
	plan, _ = dst.plan(t, text, gitops.Options{Policy: gitops.PolicyOverwrite, Env: env})
	if plan.Blocked() {
		t.Fatalf("plan blocked: %+v", plan)
	}
	if err := dst.planner().Apply(dst.Ctx, dst.Principal, plan, auth.RequestMeta{}, false); err != nil {
		fatal(t, err)
	}
	if got := dst.export(t); got != text {
		t.Fatalf("the import differs from the export:\n--- imported ---\n%s\n--- exported ---\n%s", got, text)
	}
	ch, _ := dst.Store.Channels().GetByName(dst.Ctx, "erp-hook")
	_, e, _, _ := dst.channels.Env(dst.Ctx, ch.ID)
	if e.Config["hmac_secret"] != "hook-secret-value" {
		t.Errorf("secret %v", e.Config["hmac_secret"])
	}

	// Applying the same documents again changes nothing.
	plan, _ = dst.plan(t, text, gitops.Options{Env: env})
	if got := actions(plan); strings.Count(got, "=unchanged") != 4 {
		t.Errorf("second plan %s %+v", got, plan.Items)
	}
}

func TestConflictPolicies(t *testing.T) {
	w := newWorkspace(t)
	w.seed(t)
	changed := strings.Replace(w.export(t), "cron: 0 7 * * 1-5", "cron: 30 6 * * *", 1)
	rp, _ := w.Store.Reports().GetBySlug(w.Ctx, "daily-orders")
	before, _ := w.Store.Deliveries().ListByReport(w.Ctx, rp.ID)

	plan, _ := w.plan(t, changed, gitops.Options{})
	it := plan.Items[3]
	if it.Action != gitops.ActionConflict || len(it.Changes) != 1 || it.Changes[0].Field != "spec.schedule.cron" || it.Changes[0].To != "30 6 * * *" || !plan.Blocked() {
		t.Fatalf("conflict %+v", it)
	}
	plan, _ = w.plan(t, changed, gitops.Options{Items: map[string]gitops.Policy{"Report/daily-orders": gitops.PolicySkip}})
	if plan.Items[3].Action != gitops.ActionSkip || plan.Blocked() {
		t.Fatalf("skip %+v", plan.Items[3])
	}

	plan, _ = w.plan(t, changed, gitops.Options{Policy: gitops.PolicyCopy})
	if got := actions(plan); !strings.HasSuffix(got, "Report/daily-orders=create->daily-orders-2") {
		t.Fatalf("copy %s", got)
	}
	if err := w.planner().Apply(w.Ctx, w.Principal, plan, auth.RequestMeta{}, false); err != nil {
		fatal(t, err)
	}
	if cp, err := w.Store.Reports().GetBySlug(w.Ctx, "daily-orders-2"); err != nil || cp.Cron != "30 6 * * *" {
		t.Fatalf("copy %+v %v", cp, err)
	}

	plan, _ = w.plan(t, changed, gitops.Options{Policy: gitops.PolicyOverwrite})
	if err := w.planner().Apply(w.Ctx, w.Principal, plan, auth.RequestMeta{}, false); err != nil {
		fatal(t, err)
	}
	rp, _ = w.Store.Reports().GetBySlug(w.Ctx, "daily-orders")
	after, _ := w.Store.Deliveries().ListByReport(w.Ctx, rp.ID)
	if rp.Cron != "30 6 * * *" || len(after) != 1 || after[0].ID != before[0].ID {
		t.Errorf("overwrite: cron %s, deliveries %d (kept id: %v)", rp.Cron, len(after), len(after) == 1 && after[0].ID == before[0].ID)
	}

	// Leaving deliveries out of a report removes them.
	noDeliveries := changed[:strings.Index(changed, "  deliveries:")]
	plan, _ = w.plan(t, noDeliveries, gitops.Options{Policy: gitops.PolicyOverwrite})
	if it := plan.Items[len(plan.Items)-1]; it.Action != gitops.ActionUpdate || it.Changes[0].Field != "spec.deliveries" {
		t.Fatalf("removal %+v", it)
	}
	_ = w.planner().Apply(w.Ctx, w.Principal, plan, auth.RequestMeta{}, false)
	if ds, _ := w.Store.Deliveries().ListByReport(w.Ctx, rp.ID); len(ds) != 0 {
		t.Errorf("deliveries left %d", len(ds))
	}
}

const inline = `apiVersion: rowbird.dev/v1
kind: Report
metadata: { name: big-orders, title: Big orders }
spec:
  query:
    connection: warehouse
    sql: |
      select * from orders where total > {{min}}
    params: [ { name: min, type: decimal, default: "100" } ]
  schedule: { cron: "@daily" }
`

func TestMissingReferencesMapAndInlineQueries(t *testing.T) {
	w := newWorkspace(t)
	plan, _ := w.plan(t, inline, gitops.Options{})
	if len(plan.Missing) != 1 || plan.Missing[0].Kind != "Connection" || plan.Missing[0].Name != "warehouse" || !plan.Blocked() {
		t.Fatalf("missing %+v", plan.Missing)
	}
	plan, _ = w.plan(t, inline, gitops.Options{Map: map[string]string{"warehouse": "shop"}})
	if got := actions(plan); got != "Query/big-orders=create Report/big-orders=create" || plan.Blocked() {
		t.Fatalf("mapped %s %+v", got, plan)
	}
	if err := w.planner().Apply(w.Ctx, w.Principal, plan, auth.RequestMeta{}, false); err != nil {
		fatal(t, err)
	}
	q, err := w.Store.Queries().GetBySlug(w.Ctx, "big-orders")
	if err != nil || q.Title != "Big orders" {
		t.Fatalf("inline query %+v %v", q, err)
	}
	rp, _ := w.Store.Reports().GetBySlug(w.Ctx, "big-orders")
	if rp.QueryID != q.ID || rp.Timezone != testenvTimezone {
		t.Errorf("report %+v", rp)
	}
}

const testenvTimezone = "America/Sao_Paulo"

func TestDryRunAndRollback(t *testing.T) {
	w := newWorkspace(t)
	bad := strings.Replace(inline, `"@daily"`, `"61 * * * *"`, 1)
	plan, _ := w.plan(t, bad, gitops.Options{Map: map[string]string{"warehouse": "shop"}})
	err := w.planner().Apply(w.Ctx, w.Principal, plan, auth.RequestMeta{}, true)
	es, ok := gitops.AsErrors(err)
	if !ok || !strings.Contains(es.Error(), "in.yaml:1:1: Report/big-orders: cron") {
		t.Fatalf("dry run error: %v", err)
	}
	// The query created before the report failed was rolled back too.
	if _, err := w.Store.Queries().GetBySlug(w.Ctx, "big-orders"); err == nil {
		t.Error("the failed apply left the query behind")
	}

	plan, _ = w.plan(t, inline, gitops.Options{Map: map[string]string{"warehouse": "shop"}})
	if err := w.planner().Apply(w.Ctx, w.Principal, plan, auth.RequestMeta{}, true); err != nil {
		fatal(t, err)
	}
	if _, err := w.Store.Reports().GetBySlug(w.Ctx, "big-orders"); err == nil {
		t.Error("a dry run saved the report")
	}
}

func TestSecretErrorsPointAtSecrets(t *testing.T) {
	w := newWorkspace(t)
	doc := "apiVersion: rowbird.dev/v1\nkind: Channel\nmetadata: {name: kuma}\nspec:\n  type: webhook\n  config: {url: https://example.com}\n  secrets: {hmac_secret: \"${env:HMAC}\"}\n"
	plan, _ := w.plan(t, strings.Replace(doc, "type: webhook\n  config: {url: https://example.com}", "type: webhook\n  config: {url: not-a-url}", 1), gitops.Options{})
	if len(plan.Errors) != 1 || !strings.Contains(plan.Errors[0].Message, "spec.config.url: validation.url") {
		t.Fatalf("config error %+v", plan.Errors)
	}
	kuma := "apiVersion: rowbird.dev/v1\nkind: Channel\nmetadata: {name: kuma}\nspec:\n  type: uptime_kuma\n  secrets: {push_url: \"${env:PUSH}\"}\n"
	plan, _ = w.plan(t, kuma, gitops.Options{Secrets: map[string]string{"PUSH": "not a url"}})
	if len(plan.Errors) != 1 || !strings.Contains(plan.Errors[0].Message, "spec.secrets.push_url: validation.url") {
		t.Fatalf("secret error %+v", plan.Errors)
	}
}
