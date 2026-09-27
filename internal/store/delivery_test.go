package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

func TestChannelsDeliveriesAndLinks(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctxA := newWorkspace(t, s, "a")
		ctxB := newWorkspace(t, s, "b")
		rp := newReport(t, s, ctxA, "r", nil)
		enc := "v1:k:n:c"
		ch := &store.Channel{Name: "ops-mail", Type: "email", Config: map[string]any{"host": "smtp"}, SecretsEnc: &enc, IsSystemMailer: true}
		if err := s.Channels().Create(ctxA, ch); err != nil {
			t.Fatal(err)
		}
		if err := s.Channels().Create(ctxA, &store.Channel{Name: "ops-mail", Type: "email"}); !errors.Is(err, store.ErrDuplicate) {
			t.Fatalf("duplicate name: %v", err)
		}
		other := &store.Channel{Name: "slack", Type: "slack", IsSystemMailer: true}
		_ = s.Channels().Create(ctxA, other)
		if err := s.Channels().ClearSystemMailer(ctxA, other.ID); err != nil {
			t.Fatal(err)
		}
		if m, err := s.Channels().SystemMailer(ctxA); err != nil || m.ID != other.ID {
			t.Fatalf("system mailer %v %v", m, err)
		}
		if _, err := s.Channels().Get(ctxB, ch.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("channel visible from B: %v", err)
		}
		at := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
		if prev, err := s.Channels().RecordHealth(ctxA, ch.ID, false, at, "535 auth failed"); err != nil || prev != store.ChannelUnknown {
			t.Fatalf("record health: %q %v", prev, err)
		}
		if prev, err := s.Channels().RecordHealth(ctxA, ch.ID, false, at, "535 auth failed"); err != nil || prev != store.ChannelFailing {
			t.Fatalf("record health again: %q %v", prev, err)
		}
		if _, err := s.Channels().RecordHealth(ctxB, ch.ID, true, at, ""); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("health from B: %v", err)
		}
		got, _ := s.Channels().Get(ctxA, ch.ID)
		if got.Status != store.ChannelFailing || !got.LastFailureAt.Equal(at) || *got.LastError != "535 auth failed" || got.Version != 1 || got.Config["host"] != "smtp" {
			t.Fatalf("health %+v", got)
		}

		d := &store.Delivery{
			ReportID: rp.ID, ChannelID: ch.ID, Mode: "attachment", Formats: []string{"xlsx", "csv"}, InlineRowLimit: 20,
			IncludeInlineWithFiles: true, LinkExpiresSeconds: 604800, Enabled: true, Options: map[string]any{"to": []any{"a@example.com"}},
		}
		if err := s.Deliveries().Create(ctxA, d); err != nil {
			t.Fatal(err)
		}
		list, _ := s.Deliveries().ListByReport(ctxA, rp.ID)
		if len(list) != 1 || len(list[0].Formats) != 2 || list[0].Options["to"] == nil {
			t.Fatalf("deliveries %+v", list)
		}
		if used, _ := s.Deliveries().ListByChannel(ctxA, ch.ID); len(used) != 1 {
			t.Fatalf("by channel %d", len(used))
		}
		if err := s.Channels().Delete(ctxA, ch.ID); err == nil {
			t.Fatal("deleted a channel used by a delivery")
		}

		// A run with an artifact, an attempt and a link.
		run := &store.Run{ReportID: rp.ID, Trigger: store.TriggerManual, AvailableAt: at}
		_ = s.Runs().Create(ctxA, run)
		art := &store.Artifact{RunID: run.ID, Format: "xlsx", ContentType: "application/x", FileName: "r.xlsx", StorageBackend: "local", StorageKey: "w/r/run/r.xlsx", SizeBytes: 10, SHA256: "abc"}
		if err := s.Artifacts().Create(ctxA, art); err != nil {
			t.Fatal(err)
		}
		if err := s.Artifacts().Create(ctxA, &store.Artifact{RunID: run.ID, Format: "xlsx", ContentType: "x", FileName: "x", StorageBackend: "local", StorageKey: "k", SHA256: "x"}); !errors.Is(err, store.ErrDuplicate) {
			t.Fatalf("second artifact of a format: %v", err)
		}
		att := &store.DeliveryAttempt{RunID: run.ID, DeliveryID: &d.ID, ChannelID: &ch.ID}
		if err := s.Attempts().Create(ctxA, att); err != nil || att.Status != store.AttemptPending {
			t.Fatalf("attempt %+v %v", att, err)
		}
		code := "delivery.auth_failed"
		att.Status, att.Attempts, att.LastErrorCode, att.Meta = store.AttemptFailed, 3, &code, map[string]any{"http_status": 401}
		if err := s.Attempts().Save(ctxA, att); err != nil {
			t.Fatal(err)
		}
		failed, _ := s.Attempts().FailedByChannel(ctxA, ch.ID, at.Add(-time.Hour))
		if len(failed) != 1 || failed[0].Attempts != 3 || failed[0].Meta["http_status"] == nil {
			t.Fatalf("failed attempts %+v", failed)
		}

		link := &store.SharedLink{ArtifactID: art.ID, RunID: run.ID, DeliveryID: &d.ID, TokenHash: "h1", ExpiresAt: at.Add(7 * 24 * time.Hour)}
		if err := s.Links().Create(ctxA, link); err != nil {
			t.Fatal(err)
		}
		ref, err := s.System().LinkByTokenHash(context.Background(), "h1")
		if err != nil || ref.ID != link.ID || ref.WorkspaceID != link.WorkspaceID {
			t.Fatalf("by hash %+v %v", ref, err)
		}
		if _, err := s.System().LinkByTokenHash(context.Background(), "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("unknown hash: %v", err)
		}
		dl := &store.LinkDownload{LinkID: link.ID, IP: "203.0.113.9", UserAgent: "curl"}
		if err := s.Links().RecordDownload(ctxA, dl); err != nil {
			t.Fatal(err)
		}
		gotLink, _ := s.Links().Get(ctxA, link.ID)
		if gotLink.DownloadCount != 1 || gotLink.LastDownloadAt == nil {
			t.Fatalf("link %+v", gotLink)
		}
		if logs, _ := s.Links().Downloads(ctxA, link.ID, 10); len(logs) != 1 || logs[0].IP != "203.0.113.9" {
			t.Fatalf("downloads %+v", logs)
		}
		page, _ := s.Links().List(ctxA, store.LinkFilter{ActiveAt: at}, store.PageRequest{})
		if len(page.Items) != 1 {
			t.Fatalf("active links %d", len(page.Items))
		}
		if ok, _ := s.Links().Revoke(ctxA, link.ID, ch.ID, at); !ok {
			t.Fatal("revoke")
		}
		if ok, _ := s.Links().Revoke(ctxA, link.ID, ch.ID, at); ok {
			t.Fatal("revoked twice")
		}
		if page, _ := s.Links().List(ctxA, store.LinkFilter{ActiveAt: at}, store.PageRequest{}); len(page.Items) != 0 {
			t.Fatal("revoked link listed as active")
		}

		// Deleting the report removes deliveries, runs, attempts, artifacts and links.
		if err := s.Reports().Delete(ctxA, rp.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Links().Get(ctxA, link.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("link survived: %v", err)
		}
		if err := s.Channels().Delete(ctxA, ch.ID); err != nil {
			t.Fatalf("channel no longer used: %v", err)
		}
	})
}
