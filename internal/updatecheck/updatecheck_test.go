package updatecheck_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rowbird/rowbird/internal/testenv"
	"github.com/rowbird/rowbird/internal/updatecheck"
)

func TestCheck(t *testing.T) {
	e := testenv.New(t)
	body := `{"tag_name":"v1.3.0","html_url":"https://github.com/rowbird/rowbird/releases/tag/v1.3.0","draft":false,"prerelease":false}`
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		if r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" {
			t.Errorf("the request carries more than it should: %v", r)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	for _, tc := range []struct {
		current string
		want    bool
	}{{"1.2.9", true}, {"v1.3.0", false}, {"1.4.0", false}, {"dev", false}} {
		c := updatecheck.New(e.Store, updatecheck.Options{Allowed: true, Current: tc.current, URL: srv.URL, Now: e.Clock.Now})
		if err := c.Check(t.Context()); err != nil {
			t.Fatal(err)
		}
		s := c.Status(e.Ctx)
		if s.UpdateAvailable != tc.want || s.Latest != "v1.3.0" || s.CheckedAt == nil || s.ReleaseURL == "" {
			t.Fatalf("%s: %+v", tc.current, s)
		}
		if ua != "rowbird/"+tc.current {
			t.Fatalf("user agent %q", ua)
		}
	}

	c := updatecheck.New(e.Store, updatecheck.Options{Allowed: true, Current: "1.0.0", URL: srv.URL})
	if ok, _ := c.Enabled(t.Context()); !ok {
		t.Fatal("enabled by default")
	}
	if err := e.Store.Settings().Put(e.Ctx, updatecheck.SettingEnabled, "false", false); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.Enabled(t.Context()); ok {
		t.Fatal("an admin turned it off")
	}
	_ = c.Check(t.Context())
	if s := c.Status(e.Ctx); s.UpdateAvailable || s.Enabled {
		t.Fatalf("off: %+v", s)
	}
	off := updatecheck.New(e.Store, updatecheck.Options{Allowed: false})
	if ok, _ := off.Enabled(t.Context()); ok {
		t.Fatal("ROWBIRD_UPDATE_CHECK=false")
	}

	body = `{"tag_name":"v2.0.0-rc.1","prerelease":true}`
	if err := c.Check(t.Context()); err == nil {
		t.Fatal("a prerelease was accepted")
	}
}
