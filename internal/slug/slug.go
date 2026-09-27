// Package slug derives and validates the short names that config-as-code uses to reference
// queries and reports (docs/spec/09-config-as-code.md).
package slug

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/store/ids"
)

// Limits shared by every resource with a title and a slug.
const (
	MaxLength      = 63
	MaxTitle       = 200
	MaxDescription = 2000
)

var re = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Make derives a slug from a title: accents removed, lowercase, words joined by dashes. A title
// without any ASCII letter or digit gets "<fallback>-" plus a random suffix.
func Make(title, fallback string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFD.String(strings.ToLower(title)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue // combining accent
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > MaxLength {
		s = strings.TrimRight(s[:MaxLength], "-")
	}
	if s == "" {
		s = fallback + "-" + ids.New().String()[:8]
	}
	return s
}

// Valid reports whether s is a well-formed slug.
func Valid(s string) bool { return re.MatchString(s) }

// CheckMeta validates a title, slug and description, reporting field errors under those names.
func CheckMeta(title, s, desc string) []apperr.FieldError {
	var fe []apperr.FieldError
	switch n := utf8.RuneCountInString(strings.TrimSpace(title)); {
	case n == 0:
		fe = append(fe, apperr.Field("title", "validation.required"))
	case n > MaxTitle:
		fe = append(fe, apperr.Field("title", "validation.too_long"))
	}
	if !Valid(s) {
		fe = append(fe, apperr.Field("slug", "validation.slug"))
	}
	if utf8.RuneCountInString(desc) > MaxDescription {
		fe = append(fe, apperr.Field("description", "validation.too_long"))
	}
	return fe
}
