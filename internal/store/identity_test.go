package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/ids"
	"github.com/rowbird/rowbird/internal/store/storetest"
)

func newWorkspace(t *testing.T, s *store.Store, slug string) context.Context {
	t.Helper()
	w := &store.Workspace{Name: slug, Slug: slug}
	if err := s.Workspaces().Create(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	return store.WithWorkspace(t.Context(), w.ID)
}

func newUser(t *testing.T, s *store.Store, email string) *store.User {
	t.Helper()
	hash := "$argon2id$placeholder"
	u := &store.User{Email: email, Name: "User " + email, Locale: "en", Theme: "system", PasswordHash: &hash}
	if err := s.Users().Create(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func addMember(t *testing.T, s *store.Store, ctx context.Context, u *store.User, role store.Role) *store.Member {
	t.Helper()
	m := &store.Member{UserID: u.ID, Role: role}
	if err := s.Members().Add(ctx, m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUsers(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		repo := s.Users()

		if any, err := repo.Any(ctx); err != nil || any {
			t.Fatalf("Any on empty store: %v %v", any, err)
		}
		u := newUser(t, s, "  Ana@Example.COM ")
		if u.Email != "ana@example.com" {
			t.Fatalf("email not normalized: %q", u.Email)
		}
		if any, _ := repo.Any(ctx); !any {
			t.Fatal("Any is false after creating a user")
		}
		if err := repo.Create(ctx, &store.User{Email: "ANA@example.com", Name: "x", Locale: "en", Theme: "system"}); !errors.Is(err, store.ErrDuplicate) {
			t.Fatalf("duplicate email with different case: %v", err)
		}

		got, err := repo.GetByEmail(ctx, "ana@EXAMPLE.com")
		if err != nil || got.ID != u.ID {
			t.Fatalf("GetByEmail: %v", err)
		}

		got.RecoveryCodes = []string{"h1", "h2"}
		got.MustChangePassword = true
		if err := repo.Update(ctx, got, "recovery_codes", "must_change_password"); err != nil {
			t.Fatal(err)
		}
		again, _ := repo.Get(ctx, u.ID)
		if !slices.Equal(again.RecoveryCodes, []string{"h1", "h2"}) || !again.MustChangePassword || again.Version != 2 {
			t.Fatalf("update round trip: %+v", again)
		}

		many, err := repo.GetMany(ctx, []uuid.UUID{u.ID, ids.New()})
		if err != nil || len(many) != 1 {
			t.Fatalf("GetMany: %d %v", len(many), err)
		}
	})
}

func TestLoginBookkeeping(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		repo := s.Users()
		u := newUser(t, s, "bob@example.com")

		for want := 1; want <= 3; want++ {
			n, err := repo.IncrementFailedLogins(ctx, u.ID)
			if err != nil || n != want {
				t.Fatalf("increment: %d %v", n, err)
			}
		}
		until := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
		if err := repo.SetLockedUntil(ctx, u.ID, &until); err != nil {
			t.Fatal(err)
		}
		locked, _ := repo.Get(ctx, u.ID)
		if locked.LockedUntil == nil || !locked.LockedUntil.Equal(until) || locked.Version != 1 {
			t.Fatalf("lock not stored or version bumped: %+v", locked)
		}
		if err := repo.RecordLogin(ctx, u.ID, until); err != nil {
			t.Fatal(err)
		}
		ok, _ := repo.Get(ctx, u.ID)
		if ok.FailedLogins != 0 || ok.LockedUntil != nil || ok.LastLoginAt == nil {
			t.Fatalf("RecordLogin did not reset: %+v", ok)
		}

		first, err := repo.UseTOTPStep(ctx, u.ID, 100)
		if err != nil || !first {
			t.Fatalf("first step: %v %v", first, err)
		}
		for _, step := range []int64{100, 99} {
			if used, _ := repo.UseTOTPStep(ctx, u.ID, step); used {
				t.Fatalf("step %d accepted twice", step)
			}
		}
		if next, _ := repo.UseTOTPStep(ctx, u.ID, 101); !next {
			t.Fatal("newer step rejected")
		}
	})
}

func TestMembersAreScoped(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctxA := newWorkspace(t, s, "a")
		ctxB := newWorkspace(t, s, "b")
		admin := newUser(t, s, "admin@example.com")
		viewer := newUser(t, s, "viewer@example.com")
		addMember(t, s, ctxA, admin, store.RoleAdmin)
		m := addMember(t, s, ctxA, viewer, store.RoleViewer)

		if _, err := s.Members().GetByUser(ctxB, viewer.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("membership visible from another workspace: %v", err)
		}
		if err := s.Members().Add(ctxA, &store.Member{UserID: viewer.ID, Role: store.RoleEditor}); !errors.Is(err, store.ErrDuplicate) {
			t.Fatalf("second membership in same workspace: %v", err)
		}

		n, err := s.Members().CountActiveAdmins(ctxA)
		if err != nil || n != 1 {
			t.Fatalf("admins in A: %d %v", n, err)
		}
		if n, _ := s.Members().CountActiveAdmins(ctxB); n != 0 {
			t.Fatalf("admins in B: %d", n)
		}
		m.Role = store.RoleAdmin
		if err := s.Members().Update(ctxA, m); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		viewer.DisabledAt = &now
		if err := s.Users().Update(t.Context(), viewer, "disabled_at"); err != nil {
			t.Fatal(err)
		}
		if n, _ := s.Members().CountActiveAdmins(ctxA); n != 1 {
			t.Fatalf("disabled admin counted: %d", n)
		}

		page, err := s.Members().List(ctxA, store.PageRequest{})
		if err != nil || len(page.Items) != 2 || page.NextCursor != "" {
			t.Fatalf("list: %+v %v", page, err)
		}
		if page.Items[0].User.Email != "viewer@example.com" || page.Items[1].User.Email != "admin@example.com" {
			t.Fatalf("order or join wrong: %s, %s", page.Items[0].User.Email, page.Items[1].User.Email)
		}
		if page, _ := s.Members().List(ctxB, store.PageRequest{}); len(page.Items) != 0 {
			t.Fatal("B lists A's members")
		}

		ms, err := s.Auth().MembershipsOfUser(t.Context(), admin.ID)
		if err != nil || len(ms) != 1 || ms[0].WorkspaceID != m.WorkspaceID {
			t.Fatalf("memberships: %+v %v", ms, err)
		}
	})
}

func TestPagination(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := newWorkspace(t, s, "p")
		for i := range 5 {
			if err := s.SecurityEvents().Record(ctx, &store.SecurityEvent{Type: "login_failed", Meta: map[string]any{"i": i}}); err != nil {
				t.Fatal(err)
			}
		}
		var seen []float64
		req := store.PageRequest{Limit: 2}
		for pages := 0; ; pages++ {
			page, err := s.SecurityEvents().List(ctx, store.SecurityEventFilter{}, req)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range page.Items {
				seen = append(seen, e.Meta["i"].(float64))
			}
			if page.NextCursor == "" {
				if pages != 2 {
					t.Fatalf("expected 3 pages, got %d", pages+1)
				}
				break
			}
			req.Cursor = page.NextCursor
		}
		if !slices.Equal(seen, []float64{4, 3, 2, 1, 0}) {
			t.Fatalf("pages returned %v", seen)
		}
		if _, err := s.SecurityEvents().List(ctx, store.SecurityEventFilter{}, store.PageRequest{Cursor: "not-a-cursor"}); !errors.Is(err, store.ErrInvalidCursor) {
			t.Fatalf("bad cursor: %v", err)
		}
		filtered, _ := s.SecurityEvents().List(ctx, store.SecurityEventFilter{Type: "login_success"}, store.PageRequest{})
		if len(filtered.Items) != 0 {
			t.Fatal("type filter ignored")
		}
	})
}

func TestSessionsAndChallenges(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctxA := newWorkspace(t, s, "a")
		wsID, _ := store.WorkspaceFrom(ctxA)
		u := newUser(t, s, "s@example.com")
		auth := s.Auth()
		now := time.Now().UTC().Truncate(time.Microsecond)

		mk := func(hash string) *store.Session {
			sess := &store.Session{UserID: u.ID, TokenHash: hash, ExpiresAt: now.Add(time.Hour), LastSeenAt: now, IP: "10.0.0.1", UserAgent: "test"}
			sess.WorkspaceID = wsID
			if err := auth.CreateSession(t.Context(), sess); err != nil {
				t.Fatal(err)
			}
			return sess
		}
		s1, s2 := mk("h1"), mk("h2")
		mk("h3")

		got, err := auth.SessionByTokenHash(t.Context(), "h1")
		if err != nil || got.ID != s1.ID || !got.Active(now) {
			t.Fatalf("lookup: %v", err)
		}
		if err := auth.TouchSession(t.Context(), s1.ID, now.Add(time.Minute), now.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if got, _ := auth.SessionByTokenHash(t.Context(), "h1"); !got.ExpiresAt.Equal(now.Add(2 * time.Hour)) {
			t.Fatalf("expiry not slid: %s", got.ExpiresAt)
		}
		if err := auth.RevokeSession(t.Context(), ids.New(), s2.ID, now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("revoking another user's session: %v", err)
		}
		if err := auth.RevokeSession(t.Context(), u.ID, s2.ID, now); err != nil {
			t.Fatal(err)
		}
		if err := auth.RevokeSession(t.Context(), u.ID, s2.ID, now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("double revoke: %v", err)
		}
		active, _ := auth.ListActiveSessions(t.Context(), u.ID, now)
		if len(active) != 2 || active[0].ID != s1.ID {
			t.Fatalf("active sessions: %d", len(active))
		}
		if n, _ := auth.RevokeUserSessions(t.Context(), u.ID, s1.ID, now); n != 1 {
			t.Fatalf("revoke others revoked %d", n)
		}
		if got, _ := auth.SessionByTokenHash(t.Context(), "h3"); got.Active(now) {
			t.Fatal("s3 still active")
		}
		if n, _ := auth.RevokeUserSessions(t.Context(), u.ID, uuid.Nil, now); n != 1 {
			t.Fatalf("revoke all revoked %d", n)
		}

		expired := &store.LoginChallenge{UserID: u.ID, TokenHash: "old", ExpiresAt: now.Add(-time.Minute)}
		c := &store.LoginChallenge{UserID: u.ID, TokenHash: "c1", ExpiresAt: now.Add(5 * time.Minute)}
		for _, ch := range []*store.LoginChallenge{expired, c} {
			if err := auth.CreateChallenge(t.Context(), ch); err != nil {
				t.Fatal(err)
			}
		}
		for want := 1; want <= 2; want++ {
			if n, err := auth.IncrementChallengeAttempts(t.Context(), c.ID); err != nil || n != want {
				t.Fatalf("attempts %d %v", n, err)
			}
		}
		if err := auth.DeleteChallenge(t.Context(), c, now); err != nil {
			t.Fatal(err)
		}
		for _, h := range []string{"c1", "old"} {
			if _, err := auth.ChallengeByTokenHash(t.Context(), h); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("challenge %s still present: %v", h, err)
			}
		}
	})
}

func TestAPIKeys(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctxA := newWorkspace(t, s, "a")
		ctxB := newWorkspace(t, s, "b")
		u := newUser(t, s, "k@example.com")
		now := time.Now().UTC().Truncate(time.Microsecond)

		k := &store.APIKey{UserID: u.ID, Name: "ci", Prefix: "abcd1234", KeyHash: "kh", Scopes: []string{"read", "run"}}
		if err := s.APIKeys().Create(ctxA, k); err != nil {
			t.Fatal(err)
		}
		got, err := s.Auth().APIKeyByHash(t.Context(), "kh")
		if err != nil || !slices.Equal(got.Scopes, []string{"read", "run"}) || !got.Active(now) {
			t.Fatalf("lookup: %+v %v", got, err)
		}
		if _, err := s.APIKeys().Get(ctxB, k.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("key visible from B: %v", err)
		}
		if err := s.APIKeys().Revoke(ctxB, k.ID, now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("B revoked A's key: %v", err)
		}
		if err := s.Auth().TouchAPIKey(t.Context(), k.ID, now); err != nil {
			t.Fatal(err)
		}
		if err := s.APIKeys().Revoke(ctxA, k.ID, now); err != nil {
			t.Fatal(err)
		}
		got, _ = s.APIKeys().Get(ctxA, k.ID)
		if got.Active(now) || got.LastUsedAt == nil || got.Version != 2 {
			t.Fatalf("after revoke: %+v", got)
		}

		k2 := &store.APIKey{UserID: u.ID, Name: "other", Prefix: "zz", KeyHash: "kh2", Scopes: []string{"admin"}}
		_ = s.APIKeys().Create(ctxA, k2)
		if err := s.APIKeys().RevokeAllOfUser(ctxA, u.ID, now); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.APIKeys().Get(ctxA, k2.ID); got.Active(now) {
			t.Fatal("RevokeAllOfUser left a key active")
		}
		page, _ := s.APIKeys().List(ctxA, store.PageRequest{})
		if len(page.Items) != 2 {
			t.Fatalf("list %d", len(page.Items))
		}
	})
}

func TestSettings(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctxA := newWorkspace(t, s, "a")
		ctxB := newWorkspace(t, s, "b")
		repo := s.Settings()

		if err := repo.Put(ctxA, "require_2fa", "false", false); err != nil {
			t.Fatal(err)
		}
		if err := repo.Put(ctxA, "require_2fa", "true", false); err != nil {
			t.Fatal(err)
		}
		if err := repo.Put(ctxA, "default_locale", `"pt-BR"`, false); err != nil {
			t.Fatal(err)
		}
		got, err := repo.Get(ctxA, "require_2fa")
		if err != nil || got.Value != "true" || got.Version != 2 {
			t.Fatalf("get: %+v %v", got, err)
		}
		if _, err := repo.Get(ctxB, "require_2fa"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("setting visible from B: %v", err)
		}
		all, _ := repo.List(ctxA)
		if len(all) != 2 || all[0].Key != "default_locale" || all[0].Value != `"pt-BR"` {
			t.Fatalf("list: %+v", all)
		}
	})
}
