package runner_test

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/condition"
	"github.com/rowbird/rowbird/internal/delivery"
	_ "github.com/rowbird/rowbird/internal/format/json"
	"github.com/rowbird/rowbird/internal/links"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/reports"
	"github.com/rowbird/rowbird/internal/runner"
	"github.com/rowbird/rowbird/internal/spool"
	"github.com/rowbird/rowbird/internal/store"
)

func (h *harness) channel(t *testing.T, name string, cfg map[string]any) uuid.UUID {
	t.Helper()
	cfg["name"] = name
	v, err := h.channels.Create(h.Ctx, h.Principal, channels.Input{Name: name, Type: "fakedest", Config: cfg}, auth.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func (h *harness) delivery(t *testing.T, report, channel uuid.UUID, mode string, formats []string, opts map[string]any) {
	t.Helper()
	d := &store.Delivery{
		ReportID: report, ChannelID: channel, Mode: mode, Formats: formats, Enabled: true, InlineRowLimit: 2,
		IncludeInlineWithFiles: true, LinkExpiresSeconds: 3600, Options: opts,
	}
	if err := h.Store.Deliveries().Create(h.Ctx, d); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) runNow(t *testing.T, id uuid.UUID, deliver bool) store.Run {
	t.Helper()
	takeInbox()
	run, _, err := h.reports.Run(h.Ctx, h.Principal, id, reports.RunInput{Deliver: deliver})
	if err != nil {
		t.Fatal(err)
	}
	h.process(t, h.run)
	got, _ := h.Store.Runs().Get(h.Ctx, run.ID)
	return *got
}

func byChannel(in []received) map[string]received {
	out := map[string]received{}
	for _, r := range in {
		out[r.channel] = r
	}
	return out
}

func TestDeliveriesAttachLinkAndFallBack(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select id, region, total from orders order by id", reports.Input{Title: "Pedidos"})
	big := h.channel(t, "big", map[string]any{"max_kb": 1024})
	tiny := h.channel(t, "tiny", map[string]any{"max_kb": 1})
	links := h.channel(t, "links", map[string]any{})
	h.delivery(t, rp.ID, big, plugin.ModeAttachment, []string{"xlsx", "csv"}, map[string]any{"include_rows": 3})
	h.delivery(t, rp.ID, tiny, plugin.ModeAttachment, []string{"xlsx"}, nil)
	h.delivery(t, rp.ID, links, plugin.ModeLink, []string{"csv", "json"}, nil)

	run := h.runNow(t, rp.ID, true)
	if run.Status != store.RunSuccess {
		t.Fatalf("run %s %v", run.Status, run.ErrorCode)
	}
	got := byChannel(takeInbox())
	b := got["big"]
	if len(b.msg.Attachments) != 2 || !strings.Contains(b.files[rp.Slug+"-2026-09-25-1200.csv"], "id,region,total") ||
		!strings.Contains(b.msg.Inline, "<table") || !strings.Contains(b.msg.Inline, "Showing 2 of 4") || len(b.msg.Rows) != 3 ||
		b.msg.Run.URL != "https://rb.example.com/runs/"+run.ID.String() {
		t.Errorf("big: attachments %d files %v inline %q rows %d", len(b.msg.Attachments), keys(b.files), b.msg.Inline, len(b.msg.Rows))
	}
	if tn := got["tiny"]; len(tn.msg.Attachments) != 0 || len(tn.msg.Links) != 1 || !tn.msg.FallbackNote {
		t.Errorf("tiny: %+v", tn.msg)
	}
	if l := got["links"]; len(l.msg.Links) != 2 || !strings.HasPrefix(l.msg.Links[0].URL, "https://rb.example.com/r/rbl_") {
		t.Errorf("links: %+v", l.msg.Links)
	}
	// Each format was generated once and shared.
	arts, _ := h.Store.Artifacts().ListByRun(h.Ctx, run.ID)
	files := 0
	for _, a := range arts {
		if !strings.HasPrefix(a.Format, "inline:") {
			files++
		}
		if a.SHA256 == "" || a.SizeBytes == 0 || a.StorageBackend != "local" {
			t.Errorf("artifact %+v", a)
		}
	}
	if files != 3 {
		t.Errorf("%d file artifacts, want xlsx, csv and json once each", files)
	}
	atts, _ := h.Store.Attempts().ListByRun(h.Ctx, run.ID)
	if len(atts) != 3 {
		t.Fatalf("attempts %d", len(atts))
	}
	for _, a := range atts {
		if a.Status != store.AttemptSent || a.Attempts != 1 || a.SentAt == nil {
			t.Errorf("attempt %+v", a)
		}
	}
	ch, _ := h.Store.Channels().Get(h.Ctx, tiny)
	if ch.Status != store.ChannelOK {
		t.Errorf("channel health %s", ch.Status)
	}
	if page, _ := h.Store.Links().List(h.Ctx, store.LinkFilter{RunID: run.ID}, store.PageRequest{}); len(page.Items) != 3 {
		t.Errorf("links stored: %d", len(page.Items))
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestDeliveryRetriesAndPartial(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select 1", reports.Input{})
	flaky := h.channel(t, "flaky", map[string]any{"fail_times": 2})
	broken := h.channel(t, "broken", map[string]any{"fail_auth": true, "token": "tok-very-secret"})
	h.delivery(t, rp.ID, flaky, plugin.ModeInline, nil, nil)
	h.delivery(t, rp.ID, broken, plugin.ModeInline, nil, nil)

	run := h.runNow(t, rp.ID, true)
	if run.Status != store.RunPartial || code(run) != runner.CodeDeliveryFailed {
		t.Fatalf("run %s %s", run.Status, code(run))
	}
	atts, _ := h.Store.Attempts().ListByRun(h.Ctx, run.ID)
	for _, a := range atts {
		switch *a.ChannelID {
		case flaky:
			if a.Status != store.AttemptSent || a.Attempts != 3 {
				t.Errorf("flaky %+v", a)
			}
		case broken:
			if a.Status != store.AttemptFailed || a.Attempts != 1 || *a.LastErrorCode != plugin.ErrCodeDeliveryAuth || strings.Contains(*a.LastError, "tok-very-secret") {
				t.Errorf("broken %+v %q", a, *a.LastError)
			}
		}
	}
	ch, _ := h.Store.Channels().Get(h.Ctx, broken)
	if ch.Status != store.ChannelFailing || ch.LastError == nil || strings.Contains(*ch.LastError, "tok-very-secret") {
		t.Errorf("broken channel %+v", ch)
	}
	if h.get(t, rp.ID).ConsecutiveFailures != 0 {
		t.Error("a partial run counted as a failure")
	}
}

func TestStatusDestinationsHearEveryRun(t *testing.T) {
	h := newHarness(t)
	failing := h.report(t, "select 1 -- auth", reports.Input{})
	skipped := h.report(t, "select 1", reports.Input{Condition: condIsEmpty()})
	kuma := h.channel(t, "kuma", map[string]any{"always_notify": true})
	mail := h.channel(t, "mail", map[string]any{})
	for _, id := range []uuid.UUID{failing.ID, skipped.ID} {
		h.delivery(t, id, kuma, plugin.ModeInline, nil, nil)
		h.delivery(t, id, mail, plugin.ModeInline, nil, nil)
	}
	h.runNow(t, failing.ID, true)
	got := takeInbox()
	if len(got) != 1 || got[0].channel != "kuma" || got[0].msg.Status != plugin.StatusDown || got[0].msg.Run.ErrorCode != "connection.auth_failed" {
		t.Fatalf("failed run delivered to %+v", got)
	}
	run := h.runNow(t, skipped.ID, true)
	got = takeInbox()
	if run.Status != store.RunSkipped || len(got) != 1 || got[0].msg.Status != plugin.StatusSkipped || got[0].msg.Inline != "" {
		t.Fatalf("skipped run delivered to %+v", got)
	}
	// A manual run without delivery sends nothing at all.
	h.runNow(t, skipped.ID, false)
	if got := takeInbox(); len(got) != 0 {
		t.Fatalf("preview run delivered to %+v", got)
	}
}

func TestLinksNeedBaseURL(t *testing.T) {
	h := newHarness(t)
	engine := delivery.New(h.Store, h.channels, delivery.Options{Storage: h.storage, Backend: "local", SpoolDir: h.spool, Now: h.Clock.Now, Backoff: time.Millisecond})
	noURL := runner.New(runner.Config{Tick: time.Second, SpoolDir: h.spool, InstanceID: "no-url"}, h.Store, h.Queries, h.Conns, runner.Options{Now: h.Clock.Now, Delivery: engine})
	rp := h.report(t, "select 1", reports.Input{})
	c := h.channel(t, "links", map[string]any{})
	h.delivery(t, rp.ID, c, plugin.ModeLink, []string{"csv"}, nil)
	h.run = noURL
	run := h.runNow(t, rp.ID, true)
	atts, _ := h.Store.Attempts().ListByRun(h.Ctx, run.ID)
	if run.Status != store.RunPartial || len(atts) != 1 || *atts[0].LastErrorCode != "delivery.base_url_missing" {
		t.Fatalf("run %s attempts %+v", run.Status, atts)
	}
}

func condIsEmpty() condition.Spec {
	return condition.Spec{Match: "all", Rules: []condition.Rule{{Type: "is_empty"}}}
}

func TestSharedLinks(t *testing.T) {
	h := newHarness(t)
	svc := links.NewService(h.Store, h.storage, links.Options{Now: h.Clock.Now})
	rp := h.report(t, "select id, region from orders order by id", reports.Input{Title: "Pedidos"})
	open := h.channel(t, "open", map[string]any{})
	private := h.channel(t, "private", map[string]any{})
	h.delivery(t, rp.ID, open, plugin.ModeLink, []string{"csv"}, nil)
	d := &store.Delivery{ReportID: rp.ID, ChannelID: private, Mode: plugin.ModeLink, Formats: []string{"csv"}, Enabled: true, LinkExpiresSeconds: 3600, LinkRequireLogin: true}
	_ = h.Store.Deliveries().Create(h.Ctx, d)
	h.runNow(t, rp.ID, true)
	got := byChannel(takeInbox())
	token := func(ch string) string {
		u := got[ch].msg.Links[0].URL
		return u[strings.LastIndex(u, "/")+1:]
	}
	meta := auth.RequestMeta{IP: "203.0.113.7", UserAgent: "curl/8"}
	ctx := context.Background()

	dl, err := svc.Open(ctx, token("open"), nil, meta)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()
	if !strings.HasPrefix(string(body), "id,region") || !strings.HasSuffix(dl.Name, ".csv") || dl.ContentType != "text/csv; charset=utf-8" {
		t.Fatalf("download %q %s %s", body, dl.Name, dl.ContentType)
	}
	for _, bad := range []string{"rbl_nope", "nope", token("open") + "x"} {
		if _, err := svc.Open(ctx, bad, nil, meta); !errors.Is(err, links.ErrNotFound) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := svc.Open(ctx, token("private"), nil, meta); !errors.Is(err, links.ErrLoginRequired) {
		t.Fatalf("login required: %v", err)
	}
	other := &auth.Principal{UserID: uuid.New(), WorkspaceID: uuid.New()}
	if _, err := svc.Open(ctx, token("private"), other, meta); !errors.Is(err, links.ErrLoginRequired) {
		t.Fatalf("member of another workspace: %v", err)
	}
	if dl, err := svc.Open(ctx, token("private"), h.Principal, meta); err != nil {
		t.Fatalf("signed in: %v", err)
	} else {
		_ = dl.Body.Close()
	}

	page, _ := svc.List(h.Ctx, links.Filter{}, store.PageRequest{})
	if len(page.Items) != 2 {
		t.Fatalf("links %d", len(page.Items))
	}
	var openID uuid.UUID
	for _, v := range page.Items {
		if !v.Link.RequireLogin {
			openID = v.Link.ID
		}
		if v.ReportTitle != "Pedidos" || v.Format != "csv" || v.Status != links.StatusActive {
			t.Errorf("view %+v", v)
		}
	}
	v, _ := svc.Get(h.Ctx, openID)
	if v.Link.DownloadCount != 1 || len(v.Downloads) != 1 || v.Downloads[0].IP != "203.0.113.7" {
		t.Fatalf("download log %+v", v)
	}
	if v, err := svc.Revoke(h.Ctx, h.Principal, openID); err != nil || v.Status != links.StatusRevoked || v.RevokedByName != "Ana" {
		t.Fatalf("revoke %+v %v", v, err)
	}
	if _, err := svc.Revoke(h.Ctx, h.Principal, openID); !errors.Is(err, links.ErrRevoked) {
		t.Fatalf("revoke twice: %v", err)
	}
	if _, err := svc.Open(ctx, token("open"), nil, meta); !errors.Is(err, links.ErrGone) {
		t.Fatalf("revoked link: %v", err)
	}
	h.Clock.Advance(2 * time.Hour)
	if _, err := svc.Open(ctx, token("private"), h.Principal, meta); !errors.Is(err, links.ErrGone) {
		t.Fatalf("expired link: %v", err)
	}
	if page, _ := svc.List(h.Ctx, links.Filter{ActiveOnly: true}, store.PageRequest{}); len(page.Items) != 0 {
		t.Fatalf("active links %d", len(page.Items))
	}
}

func TestPreviewDelivery(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select id, region from orders order by id", reports.Input{Title: "Pedidos"})
	c := h.channel(t, "mail", map[string]any{})
	in := reports.DeliveryInput{ChannelID: c, Mode: plugin.ModeLink, Formats: []string{"csv"}}

	// Before the first run there are no rows to show.
	p, err := h.reports.PreviewDelivery(h.Ctx, rp.ID, in)
	if err != nil || p.Body != "" {
		t.Fatalf("preview before any run: %+v %v", p, err)
	}
	h.runNow(t, rp.ID, false)
	p, err = h.reports.PreviewDelivery(h.Ctx, rp.ID, in)
	if err != nil || !strings.Contains(p.Body, "<table") || !strings.Contains(p.Body, "south") {
		t.Fatalf("preview %+v %v", p, err)
	}
	if got := takeInbox(); len(got) != 0 {
		t.Fatal("the preview sent something")
	}
	if page, _ := h.Store.Links().List(h.Ctx, store.LinkFilter{}, store.PageRequest{}); len(page.Items) != 0 {
		t.Fatal("the preview created links")
	}
	if _, err := h.reports.PreviewDelivery(h.Ctx, rp.ID, reports.DeliveryInput{ChannelID: c, Mode: "attachment"}); err == nil {
		t.Fatal("an invalid delivery was previewed")
	}
}

func TestTestDelivery(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select 1", reports.Input{Condition: condIsEmpty()})
	team := h.channel(t, "team", map[string]any{})
	other := h.channel(t, "other", map[string]any{})
	h.delivery(t, rp.ID, team, plugin.ModeAttachment, []string{"csv", "json"}, nil)
	h.delivery(t, rp.ID, other, plugin.ModeInline, nil, nil)

	test := func(channel *uuid.UUID) (store.Run, []received) {
		t.Helper()
		takeInbox()
		run, err := h.reports.TestDelivery(h.Ctx, h.Principal, rp.ID, channel)
		if err != nil {
			t.Fatal(err)
		}
		h.process(t, h.run)
		got, _ := h.Store.Runs().Get(h.Ctx, run.ID)
		return *got, takeInbox()
	}

	// Without a system mailer, a test to the caller cannot be sent.
	if _, err := h.reports.TestDelivery(h.Ctx, h.Principal, rp.ID, nil); !errors.Is(err, reports.ErrNoSystemMailer) {
		t.Fatalf("no mailer: %v", err)
	}
	// A channel the report does not deliver to is refused.
	stranger := h.channel(t, "stranger", map[string]any{})
	if _, err := h.reports.TestDelivery(h.Ctx, h.Principal, rp.ID, &stranger); err == nil {
		t.Fatal("test to a channel without a delivery was accepted")
	}

	// The condition does not hold, yet a test still delivers, and only to the chosen channel.
	run, got := test(&team)
	if run.Trigger != store.TriggerTest || len(got) != 1 || got[0].channel != "team" || !got[0].msg.Test || len(got[0].msg.Attachments) != 2 {
		t.Fatalf("channel test %s %+v", run.Trigger, got)
	}

	// The caller's email goes through the system mailer with the report's file formats.
	ch, _ := h.Store.Channels().Get(h.Ctx, stranger)
	ch.IsSystemMailer = true
	if err := h.Store.Channels().Update(h.Ctx, ch, "is_system_mailer"); err != nil {
		t.Fatal(err)
	}
	_, got = test(nil)
	if len(got) != 1 || got[0].channel != "stranger" || len(got[0].msg.Attachments) != 2 || got[0].msg.Inline == "" {
		t.Fatalf("email test %+v", got)
	}
	if h.get(t, rp.ID).ConsecutiveFailures != 0 || h.get(t, rp.ID).LastResultHash != nil {
		t.Error("a test run changed the report")
	}
}

func TestResend(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select 1", reports.Input{})
	late := h.channel(t, "late", map[string]any{"fail_times": 3})
	ok := h.channel(t, "fine", map[string]any{})
	h.delivery(t, rp.ID, late, plugin.ModeAttachment, []string{"csv"}, nil)
	h.delivery(t, rp.ID, ok, plugin.ModeInline, nil, nil)

	run := h.runNow(t, rp.ID, true)
	if run.Status != store.RunPartial {
		t.Fatalf("run %s", run.Status)
	}
	view, err := h.reports.GetRun(h.Ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var failed, sent reports.AttemptView
	for _, a := range view.Attempts {
		if a.Attempt.Status == store.AttemptFailed {
			failed = a
		} else {
			sent = a
		}
	}
	if len(view.Attempts) != 2 || !failed.Resendable() || failed.ChannelName != "late" || failed.ChannelType != "fakedest" || sent.Resendable() {
		t.Fatalf("attempts %+v", view.Attempts)
	}
	if _, err := h.reports.RetryAttempt(h.Ctx, run.ID, sent.Attempt.ID); !errors.Is(err, delivery.ErrNotFailed) {
		t.Fatalf("resend of a sent attempt: %v", err)
	}
	if _, err := h.reports.RetryAttempt(h.Ctx, uuid.New(), failed.Attempt.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("resend through another run: %v", err)
	}

	// The resend uses the stored file: the result on disk is no longer needed.
	arts, _ := h.Store.Artifacts().ListByRun(h.Ctx, run.ID)
	removeSpool(t, h, run.ID)
	takeInbox()
	got, err := h.reports.RetryAttempt(h.Ctx, run.ID, failed.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	inbox := takeInbox()
	if got.Attempt.Status != store.AttemptSent || got.Attempt.Attempts != 4 || len(inbox) != 1 || len(inbox[0].msg.Attachments) != 1 {
		t.Fatalf("resend %+v inbox %+v", got.Attempt, inbox)
	}
	if after, _ := h.Store.Artifacts().ListByRun(h.Ctx, run.ID); len(after) != len(arts) {
		t.Errorf("artifacts %d, then %d", len(arts), len(after))
	}
	if r, _ := h.Store.Runs().Get(h.Ctx, run.ID); r.Status != store.RunSuccess || r.ErrorCode != nil {
		t.Errorf("run after resend %s %v", r.Status, r.ErrorCode)
	}
}

func TestRetryFailedForChannel(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select 1", reports.Input{})
	down := h.channel(t, "down-for-a-while", map[string]any{"fail_times": 6})
	h.delivery(t, rp.ID, down, plugin.ModeInline, nil, nil)
	first := h.runNow(t, rp.ID, true)
	second := h.runNow(t, rp.ID, true)
	if first.Status != store.RunPartial || second.Status != store.RunPartial {
		t.Fatalf("runs %s %s", first.Status, second.Status)
	}
	takeInbox()
	n, err := h.reports.RetryFailed(h.Ctx, down, time.Time{})
	if err != nil || n != 2 {
		t.Fatalf("queued %d %v", n, err)
	}
	if got := takeInbox(); len(got) != 2 {
		t.Fatalf("resent %d", len(got))
	}
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		if r, _ := h.Store.Runs().Get(h.Ctx, id); r.Status != store.RunSuccess {
			t.Errorf("run %s", r.Status)
		}
	}
	if n, err := h.reports.RetryFailed(h.Ctx, down, time.Time{}); err != nil || n != 0 {
		t.Fatalf("second bulk resend queued %d %v", n, err)
	}
}

func removeSpool(t *testing.T, h *harness, runID uuid.UUID) {
	t.Helper()
	if err := os.Remove(spool.Path(h.spool, runID)); err != nil {
		t.Fatal(err)
	}
}

func TestRunDownloadUsesStoredFile(t *testing.T) {
	h := newHarness(t)
	rp := h.report(t, "select 1", reports.Input{})
	c := h.channel(t, "files", map[string]any{})
	h.delivery(t, rp.ID, c, plugin.ModeAttachment, []string{"csv"}, nil)
	run := h.runNow(t, rp.ID, true)
	view, err := h.reports.GetRun(h.Ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Only the file shows, not the inline rendering stored next to it.
	if len(view.Files) != 1 || view.Files[0].Format != "csv" {
		t.Fatalf("files %+v", view.Files)
	}
	removeSpool(t, h, run.ID)
	f, err := h.reports.RunResult(h.Ctx, h.Principal, run.ID, "csv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Body.Close() }()
	b, _ := io.ReadAll(f.Body)
	if f.Name != view.Files[0].FileName || int64(len(b)) != view.Files[0].SizeBytes || f.Size != int64(len(b)) {
		t.Fatalf("download %q %d bytes", f.Name, len(b))
	}
	// Formats that were not delivered still need the spool.
	if _, err := h.reports.RunResult(h.Ctx, h.Principal, run.ID, "json"); !errors.Is(err, reports.ErrResultUnavailable) {
		t.Fatalf("json without spool: %v", err)
	}
}
