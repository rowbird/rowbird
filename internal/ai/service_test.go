package ai_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/ai"
	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/aitest"
	"github.com/rowbird/rowbird/internal/testenv"
)

type harness struct {
	*testenv.Env
	fake *aitest.Fake
	svc  *ai.Service
}

func newHarness(t *testing.T, configure bool) *harness {
	t.Helper()
	env := testenv.New(t)
	fake := aitest.NewFake()
	svc := ai.NewService(env.Store, env.Keyring, env.Conns, ai.Options{
		Now: env.Clock.Now,
		Provider: func(id string) (plugin.AIProvider, bool) {
			return fake, id == aitest.FakeID
		},
	})
	h := &harness{Env: env, fake: fake, svc: svc}
	if configure {
		if _, err := svc.UpdateSettings(env.Ctx, env.Principal, ai.SettingsInput{Provider: aitest.FakeID, Config: map[string]any{"api_key": "sk-very-secret", "model": "m1"}}, auth.RequestMeta{}); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func code(err error) string {
	if ae, ok := apperr.As(err); ok {
		return ae.Code
	}
	return ""
}

func TestGenerateQuery(t *testing.T) {
	h := newHarness(t, true)
	// A table the assistant must never see.
	db, _ := sql.Open("sqlite", "file:"+h.ShopPath)
	_, err := db.Exec(`CREATE TABLE salaries (employee TEXT, amount DECIMAL(10,2)); INSERT INTO salaries VALUES ('Ana', 9000)`)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.Conns.RefreshSchema(h.Ctx, h.Conn.ID); err != nil {
		t.Fatal(err)
	}
	c, _ := h.Store.Connections().Get(h.Ctx, h.Conn.ID)
	c.AIExcludedTables = []string{"salaries"}
	if err := h.Store.Connections().Update(h.Ctx, c, "ai_excluded_tables"); err != nil {
		t.Fatal(err)
	}

	h.fake.Answer(`{"sql":"select region, sum(total) from orders where created_at >= {{today}} and region = {{region}} group by region","explanation":"Totais de hoje.","suggested_name":"Vendas de hoje","schedule":{"cron":"0 8 * * 1-5","timezone":""}}`, nil)
	p, err := h.svc.Generate(h.Ctx, ai.Input{Task: ai.TaskQuery, Prompt: "vendas de hoje por região, todo dia útil às 8h", ConnectionID: h.Conn.ID, SQL: "select 1", Timezone: "America/Sao_Paulo", Locale: "pt-BR"})
	if err != nil {
		t.Fatal(err)
	}
	req := h.fake.Requests()[0]
	for _, want := range []string{"SQLite", "Brazilian Portuguese", "{{today}}"} {
		if !strings.Contains(req.System, want) {
			t.Errorf("system lacks %q:\n%s", want, req.System)
		}
	}
	for _, want := range []string{"vendas de hoje", "select 1", "orders: id INTEGER", "region TEXT"} {
		if !strings.Contains(req.User, want) {
			t.Errorf("user lacks %q:\n%s", want, req.User)
		}
	}
	// Excluded tables and row data never leave the server.
	for _, never := range []string{"salaries", "employee", "9000", "south", "o'reilly"} {
		if strings.Contains(req.System+req.User, never) {
			t.Errorf("the request carries %q", never)
		}
	}
	if len(p.Params) != 1 || p.Params[0].Name != "region" || len(p.Warnings) != 0 || p.SuggestedName != "Vendas de hoje" || p.Model != "m1" {
		t.Errorf("proposal %+v", p)
	}
	if p.Schedule == nil || p.Schedule.Timezone != "America/Sao_Paulo" || p.Schedule.Description == "" || len(p.Schedule.Next) != 3 {
		t.Errorf("schedule %+v", p.Schedule)
	}
}

func TestProposalWarningsAndFailures(t *testing.T) {
	h := newHarness(t, true)
	in := ai.Input{Task: ai.TaskQuery, Prompt: "clean up", ConnectionID: h.Conn.ID, Locale: "en"}

	h.fake.Answer(`{"sql":"delete from orders; select 1","explanation":"x","suggested_name":"","schedule":{"cron":"61 * * * *","timezone":"Mars/Base"}}`, nil)
	p, err := h.svc.Generate(h.Ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Warnings, ",") != "ai.warning.multiple_statements,ai.warning.not_read_only,ai.warning.schedule_invalid" || p.Schedule != nil {
		t.Errorf("warnings %v schedule %+v", p.Warnings, p.Schedule)
	}

	for answer, want := range map[string]string{`{"explanation":"no sql"}`: plugin.ErrCodeAIInvalidOutput, `[1,2]`: plugin.ErrCodeAIInvalidOutput} {
		h.fake.Answer(answer, nil)
		if _, err := h.svc.Generate(h.Ctx, in); code(err) != want {
			t.Errorf("%s: %v", answer, err)
		}
	}
	h.fake.Answer("", &plugin.AIError{Code: plugin.ErrCodeAIAuth, Err: errors.New("bad key sk-very-secret")})
	if _, err := h.svc.Generate(h.Ctx, in); code(err) != plugin.ErrCodeAIAuth || strings.Contains(err.Error(), "sk-very-secret") {
		t.Errorf("provider failure: %v", err)
	}

	cases := []ai.Input{
		{Task: ai.TaskQuery, Prompt: " ", ConnectionID: h.Conn.ID},
		{Task: ai.TaskQuery, Prompt: "x"},
		{Task: "poem", Prompt: "x"},
		{Task: ai.TaskSchedule, Prompt: strings.Repeat("x", ai.MaxPromptChars+1)},
	}
	for _, c := range cases {
		if _, err := h.svc.Generate(h.Ctx, c); code(err) != "validation.failed" {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

func TestGenerateSchedule(t *testing.T) {
	h := newHarness(t, true)
	h.fake.Answer(`{"cron":"0 8 * * 1-5","timezone":"","explanation":"Every weekday at 8."}`, nil)
	p, err := h.svc.Generate(h.Ctx, ai.Input{Task: ai.TaskSchedule, Prompt: "weekdays at 8", Timezone: "America/Sao_Paulo", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Schedule == nil || p.Schedule.Cron != "0 8 * * 1-5" || p.Schedule.Timezone != "America/Sao_Paulo" || !strings.Contains(p.Schedule.Description, "Monday") {
		t.Errorf("schedule %+v", p.Schedule)
	}
	if req := h.fake.Requests()[0]; !strings.Contains(req.System, "America/Sao_Paulo") || !strings.Contains(req.User, "2026-09-25") {
		t.Errorf("request %+v", req)
	}
	h.fake.Answer(`{"cron":"","timezone":"","explanation":"Not possible."}`, nil)
	if p, err := h.svc.Generate(h.Ctx, ai.Input{Task: ai.TaskSchedule, Prompt: "when it rains"}); err != nil || p.Schedule != nil || p.Warnings[0] != ai.WarnNoSchedule {
		t.Errorf("no schedule %+v %v", p, err)
	}
}

func TestSettings(t *testing.T) {
	h := newHarness(t, false)
	if _, err := h.svc.Generate(h.Ctx, ai.Input{Task: ai.TaskSchedule, Prompt: "daily"}); !errors.Is(err, ai.ErrNotConfigured) {
		t.Fatalf("unconfigured: %v", err)
	}
	if _, err := h.svc.UpdateSettings(h.Ctx, h.Principal, ai.SettingsInput{Provider: aitest.FakeID, Config: map[string]any{"api_key": "sk-very-secret"}}, auth.RequestMeta{}); code(err) != "validation.failed" {
		t.Fatalf("missing model: %v", err)
	}
	if _, err := h.svc.UpdateSettings(h.Ctx, h.Principal, ai.SettingsInput{Provider: "nope"}, auth.RequestMeta{}); code(err) != "validation.failed" {
		t.Fatalf("unknown provider: %v", err)
	}
	s, err := h.svc.UpdateSettings(h.Ctx, h.Principal, ai.SettingsInput{Provider: aitest.FakeID, Config: map[string]any{"api_key": "sk-very-secret", "model": "m1"}}, auth.RequestMeta{IP: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Config["api_key"].(map[string]any); m["configured"] != true || s.Config["model"] != "m1" {
		t.Errorf("settings %+v", s)
	}
	// The key is stored encrypted only.
	all, _ := h.Store.Settings().List(h.Ctx)
	for _, st := range all {
		if strings.Contains(st.Value, "sk-very-secret") {
			t.Errorf("%s holds the key in plain text", st.Key)
		}
	}
	// A placeholder keeps the stored key.
	if _, err := h.svc.UpdateSettings(h.Ctx, h.Principal, ai.SettingsInput{Provider: aitest.FakeID, Config: map[string]any{"api_key": map[string]any{"configured": true}, "model": "m2"}}, auth.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	h.fake.Answer(`{"cron":"@daily","timezone":"UTC","explanation":"x"}`, nil)
	if _, err := h.svc.Generate(h.Ctx, ai.Input{Task: ai.TaskSchedule, Prompt: "daily"}); err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.TestSettings(h.Ctx, ai.SettingsInput{Provider: aitest.FakeID, Config: map[string]any{"api_key": map[string]any{"configured": true}, "model": "m2"}})
	if err != nil || !res.OK {
		t.Fatalf("test %+v %v", res, err)
	}
	h.fake.Answer("", &plugin.AIError{Code: plugin.ErrCodeAIAuth, Err: errors.New("key sk-very-secret refused")})
	if res, _ := h.svc.TestSettings(h.Ctx, ai.SettingsInput{Provider: aitest.FakeID, Config: map[string]any{"model": "m2"}}); res.OK || res.ErrorCode != plugin.ErrCodeAIAuth || strings.Contains(res.ErrorMessage, "sk-very-secret") {
		t.Errorf("failed test %+v", res)
	}
	// Turning the assistant off removes everything.
	if s, err := h.svc.UpdateSettings(h.Ctx, h.Principal, ai.SettingsInput{}, auth.RequestMeta{}); err != nil || s.Enabled() {
		t.Fatalf("off: %+v %v", s, err)
	}
	if all, _ := h.Store.Settings().List(h.Ctx); len(all) != 1 { // the default time zone
		t.Errorf("settings left %+v", all)
	}
}
