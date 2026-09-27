package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestGetDefaults(t *testing.T) {
	info := Get()
	if info.Version != "dev" {
		t.Errorf("Version = %q, want %q", info.Version, "dev")
	}
	if info.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", info.GoVersion, runtime.Version())
	}
	if info.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Errorf("Platform = %q", info.Platform)
	}
}

func TestGetUsesLinkerValues(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldV, oldC, oldD })
	Version, Commit, Date = "1.2.3", "abc1234", "2026-09-25T00:00:00Z"

	got := Get().String()
	for _, want := range []string{"rowbird 1.2.3", "commit abc1234", "built 2026-09-25T00:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}
