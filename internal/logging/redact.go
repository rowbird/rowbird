package logging

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/rowbird/rowbird/internal/security"
)

// sensitiveKeyParts marks an attribute key as sensitive when the lowercased key contains any of them.
var sensitiveKeyParts = []string{
	"password", "passwd", "secret", "token", "authorization", "cookie", "credential",
	"private_key", "api_key", "apikey", "master_key",
}

// sensitiveKeys are matched exactly, for keys too short to match by substring safely.
var sensitiveKeys = map[string]bool{"dsn": true, "database_url": true}

var (
	// scheme://user:password@host keeps the user and hides the password.
	urlCredentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://[^:@/\s]*):[^@/\s]*@`)
	// Keyword DSNs such as "host=db password=secret".
	keywordPassword = regexp.MustCompile(`(?i)(\bpassword\s*=\s*)('[^']*'|\S+)`)
)

// RedactString hides credentials embedded in free text, such as URLs and keyword DSNs.
func RedactString(s string) string {
	if !strings.Contains(s, "@") && !strings.Contains(strings.ToLower(s), "password") {
		return s
	}
	s = urlCredentials.ReplaceAllString(s, "$1:***@")
	return keywordPassword.ReplaceAllString(s, "${1}***")
}

// IsSensitiveKey reports whether an attribute with this key must never be logged in clear.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	if sensitiveKeys[k] {
		return true
	}
	for _, part := range sensitiveKeyParts {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

// RedactingHandler wraps another handler and removes secrets from every record: attributes with
// sensitive keys, [security.Secret] values, and credentials embedded in strings and errors.
type RedactingHandler struct {
	next slog.Handler
}

// NewRedactingHandler wraps next.
func NewRedactingHandler(next slog.Handler) *RedactingHandler {
	return &RedactingHandler{next: next}
}

func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, RedactString(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = redactAttr(a)
	}
	return &RedactingHandler{next: h.next.WithAttrs(redacted)}
}

func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{next: h.next.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, security.Redacted)
	}
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindGroup:
		group := v.Group()
		redacted := make([]slog.Attr, len(group))
		for i, g := range group {
			redacted[i] = redactAttr(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(redacted...)}
	case slog.KindString:
		return slog.String(a.Key, RedactString(v.String()))
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			msg := err.Error()
			if clean := RedactString(msg); clean != msg {
				return slog.String(a.Key, clean)
			}
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}
