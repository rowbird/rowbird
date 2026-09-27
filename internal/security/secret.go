// Package security holds small security primitives shared across packages.
package security

import (
	"log/slog"
)

// Redacted is the placeholder printed instead of a secret value.
const Redacted = "[REDACTED]"

// Secret holds a sensitive string. Every way of printing it (fmt verbs, slog, JSON, text marshaling)
// yields [Redacted]; the real value is only available through Reveal.
type Secret string

// Reveal returns the underlying value. Call it only where the value is actually used.
func (s Secret) Reveal() string { return string(s) }

// IsSet reports whether the secret holds a non-empty value.
func (s Secret) IsSet() bool { return s != "" }

func (s Secret) String() string               { return Redacted }
func (s Secret) GoString() string             { return Redacted }
func (s Secret) LogValue() slog.Value         { return slog.StringValue(Redacted) }
func (s Secret) MarshalText() ([]byte, error) { return []byte(Redacted), nil }
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }
