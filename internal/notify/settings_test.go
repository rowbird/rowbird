package notify_test

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/crypto"
	_ "github.com/rowbird/rowbird/internal/destination/email"
	_ "github.com/rowbird/rowbird/internal/destination/s3"
	_ "github.com/rowbird/rowbird/internal/destination/webhook"
	"github.com/rowbird/rowbird/internal/notify"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

func newEnv(t *testing.T, opts notify.Options) (*notify.Service, *store.Store, *channels.Service, *auth.Principal) {
	t.Helper()
	st := storetest.Open(t, "sqlite://"+filepath.Join(t.TempDir(), "n.db"))
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := &store.Workspace{Name: "w", Slug: "w"}
	if err := st.Workspaces().Create(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	u := &store.User{Email: "ana@example.com", Name: "Ana", Locale: "en", Theme: "system"}
	if err := st.Users().Create(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	ctx := store.WithWorkspace(t.Context(), w.ID)
	if err := st.Members().Add(ctx, &store.Member{UserID: u.ID, Role: store.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	kr, _ := crypto.NewKeyring(key)
	ch := channels.NewService(st, kr, channels.Options{})
	svc := notify.New(st, kr, ch, opts)
	ch.Observe(svc)
	return svc, st, ch, &auth.Principal{UserID: u.ID, WorkspaceID: w.ID, Role: store.RoleAdmin}
}

func mustChannel(t *testing.T, ch *channels.Service, p *auth.Principal, name, typ string, cfg map[string]any) *channels.View {
	t.Helper()
	v, err := ch.Create(store.WithWorkspace(t.Context(), p.WorkspaceID), p, channels.Input{Name: name, Type: typ, Config: cfg}, auth.RequestMeta{})
	if err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	return v
}

func fields(err error) map[string]string {
	out := map[string]string{}
	if ae, ok := apperr.As(err); ok {
		for _, f := range ae.Fields {
			out[f.Field] = f.Code
		}
	}
	return out
}

func TestAlertSettings(t *testing.T) {
	svc, st, ch, p := newEnv(t, notify.Options{})
	ctx := store.WithWorkspace(t.Context(), p.WorkspaceID)
	meta := auth.RequestMeta{IP: "203.0.113.9"}
	mail := mustChannel(t, ch, p, "ops-mail", "email", map[string]any{"host": "smtp.example.com", "from_address": "rowbird@example.com"})
	hook := mustChannel(t, ch, p, "ops-hook", "webhook", map[string]any{"url": "https://hooks.example.com/x"})
	hook2 := mustChannel(t, ch, p, "ops-hook-2", "webhook", map[string]any{"url": "https://hooks.example.com/y"})
	bucket := mustChannel(t, ch, p, "bucket", "s3", map[string]any{"bucket": "b", "access_key": "ak", "secret_key": "sk-secret"})

	got, err := svc.GetSettings(ctx)
	if err != nil || got.Primary != nil || got.HeartbeatConfigured || got.HeartbeatInterval != time.Minute {
		t.Fatalf("defaults %+v %v", got, err)
	}

	bad := "ftp://x"
	for name, c := range map[string]struct {
		in   notify.SettingsInput
		want map[string]string
	}{
		"unsupported and missing options": {
			notify.SettingsInput{Primary: &notify.Target{ChannelID: bucket.ID}, Fallback: &notify.Target{ChannelID: mail.ID}},
			map[string]string{"primary.channel_id": "validation.alerts_unsupported", "fallback.options.to": "validation.required"},
		},
		"same channel, range, url": {
			notify.SettingsInput{
				Primary: &notify.Target{ChannelID: hook.ID}, Fallback: &notify.Target{ChannelID: hook.ID},
				HeartbeatURL: &bad, HeartbeatInterval: 5 * time.Second,
			},
			map[string]string{"fallback.channel_id": "validation.same_channel", "heartbeat_interval_seconds": "validation.range", "heartbeat_url": "validation.url"},
		},
		"unknown channel": {
			notify.SettingsInput{Primary: &notify.Target{ChannelID: p.UserID}},
			map[string]string{"primary.channel_id": "validation.invalid_value"},
		},
	} {
		_, err := svc.UpdateSettings(ctx, p, c.in, meta)
		if got := fields(err); len(got) != len(c.want) {
			t.Errorf("%s: %v", name, got)
		} else {
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("%s: %s = %q, want %q", name, k, got[k], v)
				}
			}
		}
	}

	secretURL := "https://kuma.example.com/api/push/heartbeat-token-123"
	saved, err := svc.UpdateSettings(ctx, p, notify.SettingsInput{
		Primary:  &notify.Target{ChannelID: mail.ID, Options: map[string]any{"to": "ops@example.com"}},
		Fallback: &notify.Target{ChannelID: hook.ID}, HeartbeatURL: &secretURL, HeartbeatInterval: 2 * time.Minute,
	}, meta)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Primary.ChannelID != mail.ID || saved.Primary.Options["to"] != "ops@example.com" || saved.Fallback.ChannelID != hook.ID ||
		!saved.HeartbeatConfigured || saved.HeartbeatInterval != 2*time.Minute || saved.SameDestination {
		t.Fatalf("saved %+v", saved)
	}
	raw, _ := st.Settings().Get(ctx, store.SettingHeartbeatURL)
	if !raw.Secret || strings.Contains(raw.Value, "heartbeat-token") {
		t.Fatalf("heartbeat URL stored in the clear: %+v", raw)
	}
	if u, err := svc.HeartbeatURL(ctx); err != nil || u != secretURL {
		t.Fatalf("heartbeat url %q %v", u, err)
	}
	events, _ := st.SecurityEvents().List(ctx, store.SecurityEventFilter{}, store.PageRequest{})
	if b, _ := json.Marshal(events.Items); len(events.Items) == 0 || strings.Contains(string(b), "heartbeat-token") {
		t.Fatalf("security events: %s", b)
	}

	// The channels in use cannot be deleted and say why.
	if err := ch.Delete(ctx, p, hook.ID, meta); !errors.Is(err, channels.ErrInUse) {
		t.Fatalf("delete alert channel: %v", err)
	} else if ae, _ := apperr.As(err); len(ae.Dependents) != 1 || ae.Dependents[0].Type != "system_alert" || ae.Dependents[0].Name != "fallback" {
		t.Fatalf("dependents %+v", ae.Dependents)
	}

	// Leaving the URL out keeps it; the same type on both sides is flagged; "" removes the URL.
	kept, err := svc.UpdateSettings(ctx, p, notify.SettingsInput{Primary: &notify.Target{ChannelID: hook2.ID}, Fallback: &notify.Target{ChannelID: hook.ID}}, meta)
	if err != nil || !kept.HeartbeatConfigured || !kept.SameDestination || kept.HeartbeatInterval != time.Minute {
		t.Fatalf("kept %+v %v", kept, err)
	}
	empty := ""
	cleared, err := svc.UpdateSettings(ctx, p, notify.SettingsInput{HeartbeatURL: &empty}, meta)
	if err != nil || cleared.HeartbeatConfigured || cleared.Primary != nil || cleared.Fallback != nil {
		t.Fatalf("cleared %+v %v", cleared, err)
	}
	if u, _ := svc.HeartbeatURL(ctx); u != "" {
		t.Fatalf("url after clearing %q", u)
	}
	if err := ch.Delete(ctx, p, hook.ID, meta); err != nil {
		t.Fatalf("delete after clearing: %v", err)
	}
}
