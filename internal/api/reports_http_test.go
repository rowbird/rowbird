package api

import (
	"net/http"
	"testing"

	"github.com/rowbird/rowbird/internal/api/gen"
)

func TestReportsOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.setup()
	path := ts.sqliteFixture("shop.db")
	var conn gen.Connection
	admin.do(http.MethodPost, "/api/v1/connections", map[string]any{"name": "shop", "driver": "sqlite", "config": map[string]any{"path": path}}).decode(t, &conn)
	var q gen.Query
	admin.do(http.MethodPost, "/api/v1/queries", map[string]any{
		"title": "Orders", "connection_id": conn.Id, "sql": "select * from orders where total >= {{min}}",
		"params": []map[string]any{{"name": "min", "type": "decimal", "default": "0"}},
	}).decode(t, &q)

	// Schedule preview in Portuguese.
	var sp gen.SchedulePreview
	res := admin.do(http.MethodPost, "/api/v1/schedules/preview", map[string]any{"cron": "0 7 * * 1-5", "timezone": "America/Sao_Paulo", "count": 3, "locale": "pt-BR"})
	res.decode(t, &sp)
	if res.Code != 200 || len(sp.Next) != 3 || sp.Description != "Às 07:00, de segunda-feira a sexta-feira" {
		t.Fatalf("schedule preview: %d %s", res.Code, res.Body)
	}
	if p := admin.do(http.MethodPost, "/api/v1/schedules/preview", map[string]any{"cron": "@every 1m"}).problem(t); p.Errors == nil || (*p.Errors)[0].Field != "cron" {
		t.Fatalf("bad cron: %+v", p)
	}

	// Validation errors name the fields, including rules and overrides.
	res = admin.do(http.MethodPost, "/api/v1/reports", map[string]any{
		"title": "Daily", "query_id": q.Id, "cron": "0 7 * * *",
		"condition":       map[string]any{"match": "all", "rules": []map[string]any{{"type": "row_count", "op": "gt", "value": "many"}}},
		"param_overrides": map[string]any{"nope": "1"},
	})
	fields := map[string]string{}
	if p := res.problem(t); p.Errors != nil {
		for _, f := range *p.Errors {
			fields[f.Field] = f.Code
		}
	}
	if res.Code != 400 || fields["condition.rules[0].value"] == "" || fields["param_overrides.nope"] != "validation.param_undefined" {
		t.Fatalf("validation: %d %s", res.Code, res.Body)
	}

	var rp gen.Report
	res = admin.do(http.MethodPost, "/api/v1/reports", map[string]any{
		"title": "Pedidos diários", "query_id": q.Id, "cron": "0 7 * * *", "timezone": "UTC",
		"condition":       map[string]any{"match": "any", "rules": []map[string]any{{"type": "row_count", "op": "gte", "value": 1}}},
		"param_overrides": map[string]any{"min": "10"}, "max_rows": 500, "overlap_policy": "queue",
	})
	res.decode(t, &rp)
	if res.Code != 201 || rp.Slug != "pedidos-diarios" || rp.Status != gen.ReportStatusActive || rp.NextRunAt == nil || rp.QueryTitle != "Orders" ||
		*rp.MaxRows != 500 || rp.OverlapPolicy != gen.OverlapPolicyQueue || rp.Condition.Rules[0].AdditionalProperties["value"] != 1.0 || rp.LastRun != nil {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	id := rp.Id.String()

	res = admin.do(http.MethodPatch, "/api/v1/reports/"+id, map[string]any{"version": rp.Version, "max_rows": 0, "cron": "@hourly"})
	res.decode(t, &rp)
	if res.Code != 200 || rp.MaxRows != nil || rp.Cron != "@hourly" {
		t.Fatalf("update: %d %s", res.Code, res.Body)
	}
	res = admin.do(http.MethodPost, "/api/v1/reports/"+id+"/pause", nil)
	res.decode(t, &rp)
	if rp.Status != gen.ReportStatusPaused || rp.NextRunAt != nil || rp.PausedReason == nil || *rp.PausedReason != gen.PausedReasonManual {
		t.Fatalf("pause: %s", res.Body)
	}
	admin.do(http.MethodPost, "/api/v1/reports/"+id+"/resume", nil).decode(t, &rp)
	if rp.Status != gen.ReportStatusActive || rp.NextRunAt == nil {
		t.Fatalf("resume: %+v", rp)
	}

	// Manual runs: 202, then 200 with the same run for the same key.
	key := func(r *http.Request) { r.Header.Set("Idempotency-Key", "abc-1") }
	var run gen.RunSummary
	res = admin.do(http.MethodPost, "/api/v1/reports/"+id+"/run", map[string]any{"deliver": false}, key)
	res.decode(t, &run)
	if res.Code != 202 || run.Status != gen.RunStatusPending || run.Deliver || run.Trigger != gen.RunTriggerManual || run.TriggeredByName == nil {
		t.Fatalf("run: %d %s", res.Code, res.Body)
	}
	var again gen.RunSummary
	res = admin.do(http.MethodPost, "/api/v1/reports/"+id+"/run", nil, key)
	res.decode(t, &again)
	if res.Code != 200 || again.Id != run.Id {
		t.Fatalf("idempotent run: %d %s", res.Code, res.Body)
	}
	var page gen.RunPage
	admin.do(http.MethodGet, "/api/v1/runs?report_id="+id+"&status=pending,running&trigger=manual", nil).decode(t, &page)
	if len(page.Items) != 1 || page.Items[0].ReportTitle != "Pedidos diários" {
		t.Fatalf("runs: %+v", page)
	}
	if res := admin.do(http.MethodGet, "/api/v1/runs?status=done", nil); res.Code != 400 {
		t.Fatalf("bad status filter: %d", res.Code)
	}

	// The query and the report cannot be deleted while in use.
	res = admin.do(http.MethodDelete, "/api/v1/queries/"+q.Id.String(), nil)
	if p := res.problem(t); res.Code != 409 || p.Code != "query.in_use" || (*p.Dependents)[0].Type != "report" {
		t.Fatalf("query in use: %d %s", res.Code, res.Body)
	}
	if p := admin.do(http.MethodDelete, "/api/v1/reports/"+id, nil).problem(t); p.Code != "report.run_active" {
		t.Fatalf("delete with active run: %+v", p)
	}

	var detail gen.Run
	res = admin.do(http.MethodPost, "/api/v1/runs/"+run.Id.String()+"/cancel", nil)
	res.decode(t, &detail)
	if res.Code != 200 || detail.Status != gen.RunStatusCancelled || detail.ErrorCode == nil || *detail.ErrorCode != "run.cancelled" {
		t.Fatalf("cancel: %d %s", res.Code, res.Body)
	}
	if p := admin.do(http.MethodPost, "/api/v1/runs/"+run.Id.String()+"/cancel", nil).problem(t); p.Code != "run.not_active" || p.Status != 409 {
		t.Fatalf("cancel twice: %+v", p)
	}
	admin.do(http.MethodGet, "/api/v1/runs/"+run.Id.String(), nil).decode(t, &detail)
	if detail.Sample != nil || detail.Query != nil || detail.Params != nil {
		t.Fatalf("detail of a run that never started: %+v", detail)
	}

	var list gen.ReportList
	admin.do(http.MethodGet, "/api/v1/reports", nil).decode(t, &list)
	if len(list.Items) != 1 || list.Items[0].LastRun == nil || list.Items[0].LastRun.Id != run.Id {
		t.Fatalf("list: %+v", list)
	}
	if res := admin.do(http.MethodDelete, "/api/v1/reports/"+id, nil); res.Code != 204 {
		t.Fatalf("delete: %d %s", res.Code, res.Body)
	}
	if res := admin.do(http.MethodGet, "/api/v1/runs/"+run.Id.String(), nil); res.Code != 404 {
		t.Fatalf("run after delete: %d", res.Code)
	}
}
