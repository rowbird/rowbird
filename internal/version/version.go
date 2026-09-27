// Package version exposes build metadata injected at link time.
package version

import (
	"fmt"
	"runtime"
)

// These variables are overridden with -ldflags "-X" by the Makefile and the release pipeline.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Info is a snapshot of the build metadata.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// Get returns the build metadata of the running binary.
func Get() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// String renders the metadata on a single line, as printed by `rowbird version`.
func (i Info) String() string {
	return fmt.Sprintf("rowbird %s (commit %s, built %s, %s, %s)", i.Version, i.Commit, i.Date, i.GoVersion, i.Platform)
}
