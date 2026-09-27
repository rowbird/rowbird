package plugin

import (
	"context"
	"io"
	"time"
)

// Formatter kinds and inline targets (docs/spec/04-plugins.md).
const (
	FormatterFile   = "file"
	FormatterInline = "inline"

	InlineHTML     = "html"
	InlineMarkdown = "markdown"
	InlineText     = "text"
)

// Formatter turns a result into a file (CSV, XLSX, ...) or into the body of a message (an HTML
// table, a Markdown table, ...).
type Formatter interface {
	Plugin
	// Format reads in.Rows once and writes the output to w. Options in in.Options were validated
	// by ConfigSchema.
	Format(ctx context.Context, in FormatInput, w io.Writer) (FormatResult, error)
}

// RowIterator yields rows; the spool reader implements it.
type RowIterator interface {
	Next() bool
	Row() []any
	Err() error
}

// FormatInput is a result and the context it is rendered in.
type FormatInput struct {
	Columns []Column
	Rows    RowIterator
	// RowCount is the number of rows of the result; Truncated says the row limit cut it.
	RowCount  int64
	Truncated bool
	// Locale ("en" or "pt-BR") and Location (the report's time zone) drive how values read.
	Locale   string
	Location *time.Location
	// Title is the report's title; RunID and GeneratedAt identify the run.
	Title       string
	RunID       string
	GeneratedAt time.Time
	Options     map[string]any
	// MaxRows and MaxChars bound inline outputs (0 means no limit); LinkURL, when set, is where
	// the full result can be seen.
	MaxRows  int
	MaxChars int
	LinkURL  string
	// Logo is an optional PNG or JPEG image for document formats.
	Logo []byte
}

// FormatResult describes what was written.
type FormatResult struct {
	// Rows is how many rows the output contains.
	Rows int64
	// Cut is true when an inline output left rows or text out to fit its limits.
	Cut bool
}

// FormatterCapabilities describe a formatter to the UI and to destinations.
type FormatterCapabilities struct {
	Kind         string `json:"kind"`
	ContentType  string `json:"content_type"`
	Extension    string `json:"extension,omitempty"`
	InlineTarget string `json:"inline_target,omitempty"`
}
