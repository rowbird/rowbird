package notify_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/notify"
	"github.com/rowbird/rowbird/internal/store"
)

// hook is a webhook receiver that records alerts and can be told to fail.
type hook struct {
	mu     sync.Mutex
	fail   bool
	alerts []map[string]any
	srv    *httptest.Server
}

func newHook(t *testing.T) *hook {
	h := &hook{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var p struct {
			Event string         `json:"event"`
			Alert map[string]any `json:"alert"`
		}
		_ = json.Unmarshal(body, &p)
		if r.Header.Get("X-Rowbird-Event") == "alert" && p.Event == "alert" {
			h.alerts = append(h.alerts, p.Alert)
		}
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hook) titles() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, a := range h.alerts {
		out = append(out, a["title"].(string))
	}
	return out
}

func (h *hook) setFail(v bool) { h.mu.Lock(); h.fail = v; h.mu.Unlock() }

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestChannelAlertsGroupFallBackAndRecover(t *testing.T) {
	svc, st, ch, p := newEnv(t, notify.Options{Synchronous: true, BaseURL: "https://rb.example.com"})
	ctx := store.WithWorkspace(t.Context(), p.WorkspaceID)
	primary, fallback := newHook(t), newHook(t)
	pc := mustChannel(t, ch, p, "primary", "webhook", map[string]any{"url": primary.srv.URL})
	fc := mustChannel(t, ch, p, "fallback", "webhook", map[string]any{"url": fallback.srv.URL})
	mail := mustChannel(t, ch, p, "team-mail", "email", map[string]any{"host": "smtp.example.com", "from_address": "rb@example.com"})
	if _, err := svc.UpdateSettings(ctx, p, notify.SettingsInput{
		Primary: &notify.Target{ChannelID: pc.ID}, Fallback: &notify.Target{ChannelID: fc.ID},
	}, auth.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	events, stop := svc.Events().Subscribe(p.WorkspaceID, p.UserID)
	defer stop()

	channel, _ := st.Channels().Get(ctx, mail.ID)
	record := func(ok bool) {
		t.Helper()
		if err := ch.RecordHealth(ctx, channel, ok, "535 authentication failed"); err != nil {
			t.Fatal(err)
		}
	}
	record(false)
	record(false)
	record(false)
	page, _ := st.Notifications().List(ctx, p.UserID, false, store.PageRequest{})
	if len(page.Items) != 1 || page.Items[0].Type != notify.TypeChannelFailing || page.Items[0].Count != 3 || page.Items[0].Params["error"] != "535 authentication failed" {
		t.Fatalf("grouped notification %+v", page.Items)
	}
	if got := primary.titles(); !equal(got, []string{"Channel team-mail is failing"}) {
		t.Fatalf("primary alerts %v", got)
	}
	if primary.alerts[0]["url"] != "https://rb.example.com/channels/"+mail.ID.String() || primary.alerts[0]["severity"] != "error" {
		t.Fatalf("alert %v", primary.alerts[0])
	}

	// The primary fails: the recovery goes to the fallback, and the primary's health is untouched.
	primary.setFail(true)
	record(true)
	if got := fallback.titles(); !equal(got, []string{"Channel team-mail recovered"}) {
		t.Fatalf("fallback alerts %v", got)
	}
	if c, _ := st.Channels().Get(ctx, pc.ID); c.Status != store.ChannelUnknown {
		t.Fatalf("an alert changed the primary's health: %s", c.Status)
	}
	page, _ = st.Notifications().List(ctx, p.UserID, false, store.PageRequest{})
	if len(page.Items) != 2 || page.Items[0].Type != notify.TypeChannelRecovered || page.Items[1].ResolvedAt == nil {
		t.Fatalf("after recovery %+v", page.Items)
	}
	record(true)
	if len(fallback.titles()) != 1 {
		t.Fatal("a second success must not send another recovery")
	}

	// An alert about the primary itself goes straight to the fallback.
	primary.setFail(false)
	pch, _ := st.Channels().Get(ctx, pc.ID)
	if err := ch.RecordHealth(ctx, pch, false, "503"); err != nil {
		t.Fatal(err)
	}
	if got := fallback.titles(); len(got) != 2 || got[1] != "Channel primary is failing" || len(primary.titles()) != 1 {
		t.Fatalf("alert about the primary: fallback %v, primary %v", got, primary.titles())
	}

	var seen []string
	for {
		select {
		case e := <-events:
			seen = append(seen, e.Type)
			continue
		case <-time.After(50 * time.Millisecond):
		}
		break
	}
	health, created := 0, 0
	for _, s := range seen {
		switch s {
		case notify.EventChannelHealth:
			health++
		case notify.EventNotificationCreated:
			created++
		}
	}
	if health != 6 || created != 5 {
		t.Fatalf("events %v", seen)
	}
}

func TestReportAlerts(t *testing.T) {
	svc, st, ch, p := newEnv(t, notify.Options{Synchronous: true})
	ctx := store.WithWorkspace(t.Context(), p.WorkspaceID)
	primary := newHook(t)
	pc := mustChannel(t, ch, p, "primary", "webhook", map[string]any{"url": primary.srv.URL})
	if _, err := svc.UpdateSettings(ctx, p, notify.SettingsInput{Primary: &notify.Target{ChannelID: pc.ID}}, auth.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	if err := st.Settings().Put(ctx, auth.SettingDefaultLocale, `"pt-BR"`, false); err != nil {
		t.Fatal(err)
	}
	owner := &store.User{Email: "bruno@example.com", Name: "Bruno", Locale: "en", Theme: "system"}
	_ = st.Users().Create(t.Context(), owner)
	_ = st.Members().Add(ctx, &store.Member{UserID: owner.ID, Role: store.RoleEditor})
	rp := &store.Report{Title: "Vendas", OwnerID: &owner.ID, NotifyOwnerOnFail: true, AutoPauseAfter: 2}
	rp.ID = uuid.New()
	code := "connection.auth_failed"
	failed := &store.Run{Status: store.RunFailed, ErrorCode: &code}
	failed.ID = uuid.New()

	svc.RunFinished(ctx, failed, rp, false)
	svc.RunFinished(ctx, failed, rp, true)
	for _, u := range []uuid.UUID{p.UserID, owner.ID} {
		page, _ := st.Notifications().List(ctx, u, false, store.PageRequest{})
		if len(page.Items) != 2 || page.Items[0].Type != notify.TypeReportPaused || page.Items[1].Type != notify.TypeReportFailing || page.Items[1].Count != 2 {
			t.Fatalf("notifications of %s: %+v", u, page.Items)
		}
	}
	if got := primary.titles(); !equal(got, []string{"O relatório Vendas falhou", "O relatório Vendas foi pausado"}) {
		t.Fatalf("alerts %v", got)
	}

	svc.ReportResumed(ctx, rp.ID)
	ok := &store.Run{Status: store.RunSuccess}
	ok.ID = uuid.New()
	svc.RunFinished(ctx, ok, rp, false)
	svc.RunFinished(ctx, ok, rp, false)
	page, _ := st.Notifications().List(ctx, owner.ID, false, store.PageRequest{})
	if len(page.Items) != 3 || page.Items[0].Type != notify.TypeReportRecovered {
		t.Fatalf("after recovery %+v", page.Items)
	}
	for _, n := range page.Items {
		if n.ResolvedAt == nil {
			t.Errorf("%s still open", n.Type)
		}
	}
	if got := primary.titles(); len(got) != 3 || got[2] != "O relatório Vendas voltou a funcionar" {
		t.Fatalf("alerts %v", got)
	}
}
