package maintenance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/maintenance"
	"github.com/rowbird/rowbird/internal/plugin"
	localstorage "github.com/rowbird/rowbird/internal/storage/local"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

var now = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

type fixture struct {
	s       *store.Store
	ctx     context.Context
	files   plugin.Storage
	report  uuid.UUID
	user    uuid.UUID
	created int
}

func setup(t *testing.T, s *store.Store, slug string) *fixture {
	t.Helper()
	w := &store.Workspace{Name: slug, Slug: slug}
	if err := s.Workspaces().Create(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	ctx := store.WithWorkspace(t.Context(), w.ID)
	u := &store.User{Email: slug + "@example.com", Name: slug, Locale: "en", Theme: "system"}
	if err := s.Users().Create(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	conn := &store.Connection{Name: "db", Driver: "sqlite", Config: map[string]any{}}
	if err := s.Connections().Create(ctx, conn); err != nil {
		t.Fatal(err)
	}
	q := &store.Query{Title: "q", Slug: "q", ConnectionID: conn.ID}
	if err := s.Queries().Create(ctx, q); err != nil {
		t.Fatal(err)
	}
	rp := &store.Report{
		Title: "r", Slug: "r", QueryID: q.ID, Cron: "* * * * *", Timezone: "UTC", Condition: json.RawMessage(`{"match":"all","rules":[]}`),
		MisfirePolicy: store.MisfireRunOnce, OverlapPolicy: store.OverlapSkip,
	}
	if err := s.Reports().Create(ctx, rp); err != nil {
		t.Fatal(err)
	}
	files, err := localstorage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{s: s, ctx: ctx, files: files, report: rp.ID, user: u.ID}
}

// run creates a finished run of the given age with one stored artifact.
func (f *fixture) run(t *testing.T, age time.Duration, status string) (uuid.UUID, *store.Artifact) {
	t.Helper()
	at := now.Add(-age)
	r := &store.Run{ReportID: f.report, Trigger: store.TriggerManual, AvailableAt: at}
	r.CreatedAt = at
	if err := f.s.Runs().Create(f.ctx, r); err != nil {
		t.Fatal(err)
	}
	sc, _ := f.s.Scoped(f.ctx)
	if _, err := sc.NewUpdate((*store.Run)(nil)).Set("status = ?", status).Where("ru.id = ?", r.ID).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.created++
	key := "w/" + r.ID.String() + "/r.csv"
	if err := f.files.Put(t.Context(), key, bytes.NewReader([]byte("id\n1\n")), plugin.ObjectMeta{ContentType: "text/csv"}); err != nil {
		t.Fatal(err)
	}
	a := &store.Artifact{RunID: r.ID, Format: "csv", ContentType: "text/csv", FileName: "r.csv", StorageBackend: "local", StorageKey: key, SizeBytes: 5, SHA256: "x"}
	a.CreatedAt = at
	if err := f.s.Artifacts().Create(f.ctx, a); err != nil {
		t.Fatal(err)
	}
	return r.ID, a
}

func (f *fixture) fileExists(t *testing.T, a *store.Artifact) bool {
	t.Helper()
	rc, _, err := f.files.Get(t.Context(), a.StorageKey)
	if err == nil {
		_ = rc.Close()
		return true
	}
	if !errors.Is(err, plugin.ErrObjectNotFound) {
		t.Fatal(err)
	}
	return false
}

func TestRetention(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		f := setup(t, s, "a")
		other := setup(t, s, "b")
		day := 24 * time.Hour

		oldRun, oldArt := f.run(t, 100*day, store.RunSuccess)        // past run retention (90)
		midRun, midArt := f.run(t, 40*day, store.RunFailed)          // file past artifact retention (30)
		newRun, newArt := f.run(t, 2*day, store.RunSuccess)          // kept
		stuckRun, _ := f.run(t, 100*day, store.RunRunning)           // not final: kept
		otherRun, otherArt := other.run(t, 40*day, store.RunSuccess) // B keeps files for 60 days
		if err := s.Settings().Put(other.ctx, store.SettingRetentionArtifactsDays, "60", false); err != nil {
			t.Fatal(err)
		}

		ev := &store.SecurityEvent{Type: "login_success"}
		ev.CreatedAt = now.Add(-400 * day)
		if err := s.SecurityEvents().Record(f.ctx, ev); err != nil {
			t.Fatal(err)
		}
		if err := s.SecurityEvents().Record(f.ctx, &store.SecurityEvent{Type: "login_success"}); err != nil {
			t.Fatal(err)
		}
		oldResolved := now.Add(-100 * day)
		for _, n := range []*store.Notification{
			{UserID: f.user, Type: "x", Severity: store.SeverityError, TitleKey: "x", ResolvedAt: &oldResolved, LastAt: oldResolved},
			{UserID: f.user, Type: "y", Severity: store.SeverityError, TitleKey: "y", LastAt: oldResolved, GroupKey: "open"},
		} {
			if _, _, err := s.Notifications().Record(f.ctx, n); err != nil {
				t.Fatal(err)
			}
		}
		sess := &store.Session{UserID: f.user, TokenHash: "h1", ExpiresAt: now.Add(-40 * day), LastSeenAt: now.Add(-41 * day)}
		sess.WorkspaceID, _ = store.WorkspaceFrom(f.ctx)
		if err := s.Auth().CreateSession(t.Context(), sess); err != nil {
			t.Fatal(err)
		}
		live := &store.Session{UserID: f.user, TokenHash: "h2", ExpiresAt: now.Add(day), LastSeenAt: now}
		live.WorkspaceID = sess.WorkspaceID
		if err := s.Auth().CreateSession(t.Context(), live); err != nil {
			t.Fatal(err)
		}

		job := maintenance.New(s, maintenance.Options{Storage: f.files, Backend: "local", Now: func() time.Time { return now }})
		res, ran, err := job.RunIfDue(t.Context())
		if err != nil || !ran {
			t.Fatalf("run: %v %v", ran, err)
		}
		if res.Runs != 1 || res.ArtifactFiles != 3 || res.SecurityEvents != 1 || res.Notifications != 1 || res.Sessions != 1 {
			t.Fatalf("result %+v", res)
		}
		if _, err := s.Runs().Get(f.ctx, oldRun); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("old run kept: %v", err)
		}
		if f.fileExists(t, oldArt) || f.fileExists(t, midArt) || !f.fileExists(t, newArt) || !other.fileExists(t, otherArt) {
			t.Fatal("wrong files removed")
		}
		for _, id := range []uuid.UUID{midRun, newRun, stuckRun} {
			if _, err := s.Runs().Get(f.ctx, id); err != nil {
				t.Fatalf("run %s removed: %v", id, err)
			}
		}
		if _, err := s.Runs().Get(other.ctx, otherRun); err != nil {
			t.Fatal(err)
		}
		mid, err := s.Artifacts().Get(f.ctx, midArt.ID)
		if err != nil || mid.DeletedAt == nil {
			t.Fatalf("artifact row of an expired file: %+v %v", mid, err)
		}
		if _, err := s.Auth().SessionByTokenHash(t.Context(), "h2"); err != nil {
			t.Fatal("live session removed")
		}

		// Within the day, neither this instance nor another one runs it again.
		again := maintenance.New(s, maintenance.Options{Now: func() time.Time { return now.Add(time.Hour) }})
		if _, ran, err := again.RunIfDue(t.Context()); ran || err != nil {
			t.Fatalf("ran twice in a day: %v %v", ran, err)
		}
		last, err := s.Maintenance().JobLastRun(t.Context(), maintenance.JobRetention)
		if err != nil || last == nil || !last.Equal(now) {
			t.Fatalf("last run %v %v", last, err)
		}
		tomorrow := maintenance.New(s, maintenance.Options{Now: func() time.Time { return now.Add(25 * time.Hour) }})
		if _, ran, err := tomorrow.RunIfDue(t.Context()); !ran || err != nil {
			t.Fatalf("next day: %v %v", ran, err)
		}
	})
}

func TestLeaseIsExclusive(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		m := s.Maintenance()
		ok1, err1 := m.AcquireJob(t.Context(), "j", "one", now, 24*time.Hour, time.Hour)
		ok2, err2 := m.AcquireJob(t.Context(), "j", "two", now, 24*time.Hour, time.Hour)
		if err1 != nil || err2 != nil || !ok1 || ok2 {
			t.Fatalf("%v %v %v %v", ok1, err1, ok2, err2)
		}
		// A lease left by a crashed instance expires.
		if ok, _ := m.AcquireJob(t.Context(), "j", "two", now.Add(2*time.Hour), 24*time.Hour, time.Hour); !ok {
			t.Fatal("expired lease not taken over")
		}
		// The first owner can no longer finish the job.
		if err := m.FinishJob(t.Context(), "j", "one", now); err != nil {
			t.Fatal(err)
		}
		if last, _ := m.JobLastRun(t.Context(), "j"); last != nil {
			t.Fatal("a stale owner finished the job")
		}
	})
}
