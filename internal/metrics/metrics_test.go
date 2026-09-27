package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type source struct{}

func (source) CountPendingRuns(context.Context) (int, error) { return 3, nil }
func (source) ArtifactBytes(context.Context) (int64, error)  { return 2048, nil }

func scrape(t *testing.T, h http.Handler, auth string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestMetrics(t *testing.T) {
	m := New(source{}, "1.2.3", nil)
	rows := int64(42)
	m.RunFinished("success", "schedule", 1500*time.Millisecond, &rows)
	m.RunFinished("failed", "manual", time.Second, nil)
	m.DeliverySent("email", true, time.Second)
	m.DeliverySent("email", false, 2*time.Second)
	m.SchedulerLag(-time.Second)

	_, body := scrape(t, m.Handler(""), "")
	for _, want := range []string{
		`rowbird_runs_total{status="success",trigger="schedule"} 1`,
		`rowbird_runs_total{status="failed",trigger="manual"} 1`,
		`rowbird_run_duration_seconds_count 2`,
		`rowbird_query_rows_count 1`,
		`rowbird_deliveries_total{destination="email",status="failed"} 1`,
		`rowbird_delivery_duration_seconds_count{destination="email"} 2`,
		`rowbird_scheduler_lag_seconds 0`,
		`rowbird_queue_pending 3`,
		`rowbird_storage_bytes 2048`,
		`rowbird_build_info{version="1.2.3"} 1`,
		`go_goroutines`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q", want)
		}
	}

	var none *Metrics
	none.RunFinished("success", "schedule", time.Second, nil)
	none.DeliverySent("email", true, time.Second)
	none.SchedulerLag(time.Second)
}

func TestMetricsToken(t *testing.T) {
	h := New(source{}, "dev", nil).Handler("s3cr3t-token")
	for auth, want := range map[string]int{"": 401, "Bearer wrong": 401, "s3cr3t-token": 401, "Bearer s3cr3t-token": 200} {
		if code, _ := scrape(t, h, auth); code != want {
			t.Errorf("%q: %d, want %d", auth, code, want)
		}
	}
}
