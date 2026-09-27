// Package logging configures the process logger. Every logger built here redacts secrets.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Formats accepted by New.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// ParseLevel converts debug, info, warn or error into a slog level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q (want debug, info, warn or error)", s)
}

// New builds a redacting logger that writes to w in the given format and level.
func New(w io.Writer, level, format string) (*slog.Logger, error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	switch strings.ToLower(format) {
	case FormatText:
		h = slog.NewTextHandler(w, opts)
	case FormatJSON:
		h = slog.NewJSONHandler(w, opts)
	default:
		return nil, fmt.Errorf("unknown log format %q (want text or json)", format)
	}
	return slog.New(NewRedactingHandler(h)), nil
}
