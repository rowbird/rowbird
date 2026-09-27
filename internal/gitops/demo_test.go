package gitops_test

import (
	"testing"

	"github.com/rowbird/rowbird/internal/auth"
	_ "github.com/rowbird/rowbird/internal/connector/postgres"
	_ "github.com/rowbird/rowbird/internal/destination/email"
	_ "github.com/rowbird/rowbird/internal/format/inline"
	_ "github.com/rowbird/rowbird/internal/format/pdf"
)

// TestDemoConfigApplies keeps deploy/demo/config valid: the demo compose applies it as its GitOps
// directory, so a change to the YAML format or to a plugin schema must not break it silently.
func TestDemoConfigApplies(t *testing.T) {
	w := newWorkspace(t)
	env := func(name string) (string, bool) { return "demo-reader-password", name == "DEMO_SHOP_PASSWORD" }
	res, err := w.planner().ApplyDir(w.Ctx, w.Principal, "../../deploy/demo/config", env, auth.RequestMeta{})
	if err != nil {
		if res != nil && res.Plan != nil {
			t.Logf("errors: %v, missing: %+v, secrets: %+v", res.Plan.Errors, res.Plan.Missing, res.Plan.Secrets)
		}
		fatal(t, err)
	}
	for _, slug := range []string{"sales-pulse", "low-stock-alert", "weekly-top-customers", "monthly-revenue-by-category"} {
		rp, err := w.Store.Reports().GetBySlug(w.Ctx, slug)
		if err != nil {
			t.Fatalf("report %s: %v", slug, err)
		}
		if !rp.Enabled {
			t.Errorf("report %s is not enabled", slug)
		}
	}
}
