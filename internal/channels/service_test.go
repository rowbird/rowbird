package channels_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/crypto"
	_ "github.com/rowbird/rowbird/internal/destination/email"
	_ "github.com/rowbird/rowbird/internal/destination/webhook"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

const hmacSecret = "whsec-super-secret"

// fakeHook answers webhook requests with a settable status and remembers the last event.
type fakeHook struct {
	status int
	event  string
}

func setup(t *testing.T) (*channels.Service, *store.Store, *auth.Principal, *fakeHook, string) {
	t.Helper()
	st := storetest.Open(t, "sqlite://"+filepath.Join(t.TempDir(), "c.db"))
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := &store.Workspace{Name: "w", Slug: "w"}
	_ = st.Workspaces().Create(t.Context(), w)
	u := &store.User{Email: "ana@example.com", Name: "Ana", Locale: "en", Theme: "system"}
	_ = st.Users().Create(t.Context(), u)
	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	kr, _ := crypto.NewKeyring(key)
	hook := &fakeHook{status: 200}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hook.event = r.Header.Get("X-Rowbird-Event")
		w.WriteHeader(hook.status)
	}))
	t.Cleanup(srv.Close)
	p := &auth.Principal{UserID: u.ID, WorkspaceID: w.ID, Role: store.RoleAdmin}
	return channels.NewService(st, kr, channels.Options{}), st, p, hook, srv.URL
}

func createReport(t *testing.T, st *store.Store, ctx context.Context) *store.Report {
	t.Helper()
	conn := &store.Connection{Name: "db", Driver: "sqlite", Config: map[string]any{}}
	_ = st.Connections().Create(ctx, conn)
	q := &store.Query{Title: "q", Slug: "q", ConnectionID: conn.ID}
	_ = st.Queries().Create(ctx, q)
	rp := &store.Report{
		Title: "Daily", Slug: "daily", QueryID: q.ID, Cron: "@daily", Timezone: "UTC", Condition: json.RawMessage(`{"match":"all","rules":[]}`),
		MisfirePolicy: store.MisfireRunOnce, OverlapPolicy: store.OverlapSkip,
	}
	if err := st.Reports().Create(ctx, rp); err != nil {
		t.Fatal(err)
	}
	return rp
}

func TestChannels(t *testing.T) {
	svc, st, p, hook, url := setup(t)
	ctx := store.WithWorkspace(t.Context(), p.WorkspaceID)
	meta := auth.RequestMeta{IP: "203.0.113.1"}

	_, err := svc.Create(ctx, p, channels.Input{Name: "Bad Name", Type: "webhook", Config: map[string]any{"url": "ftp://x"}}, meta)
	ae, ok := apperr.As(err)
	if !ok || len(ae.Fields) != 2 {
		t.Fatalf("validation: %v", err)
	}
	if _, err := svc.Create(ctx, p, channels.Input{Name: "x", Type: "nope"}, meta); !errors.Is(err, channels.ErrUnknownType) {
		t.Fatalf("unknown type: %v", err)
	}
	if _, err := svc.Create(ctx, p, channels.Input{Name: "x", Type: "webhook", Config: map[string]any{"url": url}, IsSystemMailer: true}, meta); err == nil {
		t.Fatal("a webhook became the system mailer")
	}

	v, err := svc.Create(ctx, p, channels.Input{Name: "ops-hook", Type: "webhook", Config: map[string]any{"url": url, "hmac_secret": hmacSecret}}, meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := v.Config["hmac_secret"]; leaked || !v.SecretsConfigured["hmac_secret"] || v.SecretsConfigured["secret_headers"] || v.SecretsEnc != nil {
		t.Fatalf("view %+v %v", v.Config, v.SecretsConfigured)
	}
	stored, _ := st.Channels().Get(ctx, v.ID)
	b, _ := json.Marshal(stored)
	if strings.Contains(string(b), hmacSecret) {
		t.Fatal("secret stored in clear")
	}
	if _, err := svc.Create(ctx, p, channels.Input{Name: "ops-hook", Type: "webhook", Config: map[string]any{"url": url}}, meta); !errors.Is(err, channels.ErrNameTaken) {
		t.Fatalf("name taken: %v", err)
	}

	// Placeholder keeps the secret; the destination still signs.
	v, err = svc.Update(ctx, p, v.ID, channels.Patch{Version: v.Version, Config: map[string]any{"url": url + "/v2", "hmac_secret": map[string]any{"configured": true}}}, meta)
	if err != nil {
		t.Fatal(err)
	}
	d, envv, _, err := svc.Env(ctx, v.ID)
	if err != nil || envv.Config["hmac_secret"] != hmacSecret || envv.Config["url"] != url+"/v2" || d.Meta().ID != "webhook" {
		t.Fatalf("env %v %v", envv.Config, err)
	}

	// Tests record the channel's health, and failures never carry the secret.
	res, err := svc.TestSaved(ctx, v.ID, "", "en")
	if err != nil || !res.OK || hook.event != "test" {
		t.Fatalf("test %+v %v", res, err)
	}
	got, _ := st.Channels().Get(ctx, v.ID)
	if got.Status != store.ChannelOK || got.LastSuccessAt == nil {
		t.Fatalf("health %+v", got)
	}
	hook.status = 401
	res, _ = svc.TestSaved(ctx, v.ID, "", "en")
	got, _ = st.Channels().Get(ctx, v.ID)
	if res.OK || res.ErrorCode != "delivery.auth_failed" || got.Status != store.ChannelFailing || strings.Contains(res.ErrorMessage, hmacSecret) {
		t.Fatalf("failed test %+v %+v", res, got)
	}
	hook.status = 200
	// An unsaved form is tested with the saved secrets.
	res, err = svc.Test(ctx, channels.TestInput{ChannelID: &v.ID, Type: "webhook", Config: map[string]any{"url": url, "hmac_secret": map[string]any{"configured": true}}})
	if err != nil || !res.OK {
		t.Fatalf("unsaved test %+v %v", res, err)
	}

	// A channel used by a delivery cannot be deleted.
	rp := createReport(t, st, ctx)
	_ = st.Deliveries().Create(ctx, &store.Delivery{ReportID: rp.ID, ChannelID: v.ID, Mode: "inline", Enabled: true})
	err = svc.Delete(ctx, p, v.ID, meta)
	if ae, ok := apperr.As(err); !ok || ae.Code != "channel.in_use" || len(ae.Dependents) != 1 || ae.Dependents[0].Name != "Daily" {
		t.Fatalf("delete in use: %v", err)
	}
	list, _ := svc.List(ctx)
	if len(list) != 1 || len(list[0].UsedBy) != 1 {
		t.Fatalf("list %+v", list)
	}

	// One system mailer at a time.
	m1, err := svc.Create(ctx, p, channels.Input{Name: "mail-1", Type: "email", Config: map[string]any{"host": "smtp.example.com", "from_address": "a@example.com", "password": "pw-secret"}, IsSystemMailer: true}, meta)
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := svc.Create(ctx, p, channels.Input{Name: "mail-2", Type: "email", Config: map[string]any{"host": "smtp.example.com", "from_address": "b@example.com"}, IsSystemMailer: true}, meta)
	if got, _ := svc.Get(ctx, m1.ID); got.IsSystemMailer || !m2.IsSystemMailer {
		t.Fatal("two system mailers")
	}
	events, _ := st.SecurityEvents().List(ctx, store.SecurityEventFilter{}, store.PageRequest{})
	for _, e := range events.Items {
		if b, _ := json.Marshal(e.Meta); strings.Contains(string(b), "pw-secret") || strings.Contains(string(b), hmacSecret) {
			t.Fatal("secret in a security event")
		}
	}
}
