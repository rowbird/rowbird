// Package updatecheck asks GitHub, once a day, for the latest Rowbird release (docs/spec/08-operations.md,
// "Privacy"). It is the only outbound call Rowbird makes on its own. The request carries no
// identifier besides the User-Agent with the running version, and it is off when
// ROWBIRD_UPDATE_CHECK is false or when an admin turns it off in Settings > About.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/rowbird/rowbird/internal/store"
)

// DefaultURL is the GitHub API endpoint of the latest release.
const DefaultURL = "https://api.github.com/repos/rowbird/rowbird/releases/latest"

// SettingEnabled is the workspace setting an admin uses to turn the check off (true by default).
const SettingEnabled = store.SettingUpdateCheck

// Interval is how often the check runs.
const Interval = 24 * time.Hour

// Status is what the About page shows.
type Status struct {
	Current string
	// Allowed is false when ROWBIRD_UPDATE_CHECK turned the check off for the instance.
	Allowed bool
	// Enabled is the admin's setting (only meaningful when Allowed).
	Enabled         bool
	Latest          string
	ReleaseURL      string
	CheckedAt       *time.Time
	UpdateAvailable bool
}

// Checker runs the daily check and keeps the latest answer in memory.
type Checker struct {
	store   *store.Store
	client  *http.Client
	url     string
	current string
	allowed bool
	logger  *slog.Logger
	now     func() time.Time

	mu        sync.Mutex
	latest    string
	release   string
	checkedAt *time.Time
}

// Options configure a Checker.
type Options struct {
	// Allowed mirrors ROWBIRD_UPDATE_CHECK.
	Allowed bool
	Current string
	URL     string
	Client  *http.Client
	Logger  *slog.Logger
	Now     func() time.Time
}

// New builds a checker.
func New(st *store.Store, opts Options) *Checker {
	c := &Checker{store: st, client: opts.Client, url: opts.URL, current: opts.Current, allowed: opts.Allowed, logger: opts.Logger, now: opts.Now}
	if c.client == nil {
		c.client = &http.Client{Timeout: 15 * time.Second}
	}
	if c.url == "" {
		c.url = DefaultURL
	}
	if c.logger == nil {
		c.logger = slog.New(slog.DiscardHandler)
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c
}

// Enabled reports whether the check may run: allowed on the instance and not turned off in any
// workspace.
func (c *Checker) Enabled(ctx context.Context) (bool, error) {
	if !c.allowed {
		return false, nil
	}
	workspaces, err := c.store.Workspaces().List(ctx)
	if err != nil {
		return false, err
	}
	for _, w := range workspaces {
		if !EnabledIn(store.WithWorkspace(ctx, w.ID), c.store) {
			return false, nil
		}
	}
	return true, nil
}

// EnabledIn reads the workspace setting in ctx (true when unset).
func EnabledIn(ctx context.Context, st *store.Store) bool {
	s, err := st.Settings().Get(ctx, SettingEnabled)
	if err != nil {
		return true
	}
	var v bool
	if json.Unmarshal([]byte(s.Value), &v) != nil {
		return true
	}
	return v
}

// Run checks once a day while enabled.
func (c *Checker) Run(ctx context.Context) {
	if !c.allowed {
		return
	}
	// A short delay keeps the check out of the way of startup.
	wait := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = Interval
		if ok, err := c.Enabled(ctx); err != nil || !ok {
			continue
		}
		if err := c.Check(ctx); err != nil && ctx.Err() == nil {
			c.logger.InfoContext(ctx, "update check failed", "error", err)
		}
	}
}

type release struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Check asks for the latest release now.
func (c *Checker) Check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "rowbird/"+c.current)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update check: %s answered %d", c.url, resp.StatusCode)
	}
	var r release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return fmt.Errorf("update check: %w", err)
	}
	tag := canonical(r.TagName)
	if tag == "" || r.Draft || r.Prerelease {
		return errors.New("update check: the latest release has no usable version")
	}
	if !strings.HasPrefix(r.HTMLURL, "https://") {
		r.HTMLURL = ""
	}
	at := c.now().UTC()
	c.mu.Lock()
	c.latest, c.release, c.checkedAt = tag, r.HTMLURL, &at
	c.mu.Unlock()
	return nil
}

// Status returns the latest answer.
func (c *Checker) Status(ctx context.Context) Status {
	c.mu.Lock()
	latest := c.latest
	// Shown like the running version, which carries no "v" ("1.2.3").
	s := Status{Current: c.current, Allowed: c.allowed, Latest: strings.TrimPrefix(latest, "v"), ReleaseURL: c.release, CheckedAt: c.checkedAt}
	c.mu.Unlock()
	s.Enabled = EnabledIn(ctx, c.store)
	cur := canonical(c.current)
	s.UpdateAvailable = s.Allowed && s.Enabled && cur != "" && latest != "" && semver.Compare(latest, cur) > 0
	return s
}

// canonical turns "1.2.3" or "v1.2.3" into "v1.2.3"; "" when it is not a version.
func canonical(v string) string {
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return semver.Canonical(v)
}
