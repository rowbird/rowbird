package auth

import (
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/i18n"
	"github.com/rowbird/rowbird/internal/store"
)

// Validation codes shared by the service (the password ones live in password.go).
const (
	CodeRequired     = "validation.required"
	CodeInvalidEmail = "validation.email"
	CodeInvalidValue = "validation.invalid_value"
)

const maxNameLength = 100

// fieldErrors accumulates validation problems for one request.
type fieldErrors []apperr.FieldError

func (f *fieldErrors) add(field, code string) { *f = append(*f, apperr.Field(field, code)) }

func (f fieldErrors) err() error {
	if len(f) == 0 {
		return nil
	}
	return apperr.Invalid(f...)
}

func (f *fieldErrors) email(field, v string) string {
	v = store.NormalizeEmail(v)
	if v == "" {
		f.add(field, CodeRequired)
		return v
	}
	addr, err := mail.ParseAddress(v)
	if err != nil || addr.Address != v || len(v) > 254 {
		f.add(field, CodeInvalidEmail)
	}
	return v
}

func (f *fieldErrors) name(field, v string) string {
	v = strings.TrimSpace(v)
	switch n := utf8.RuneCountInString(v); {
	case n == 0:
		f.add(field, CodeRequired)
	case n > maxNameLength:
		f.add(field, CodeTooLong)
	}
	return v
}

func (f *fieldErrors) password(field, v, email string) {
	if err := ValidatePassword(v, email); err != nil {
		if pe, ok := err.(*PolicyError); ok { //nolint:errorlint // ValidatePassword returns the type directly
			f.add(field, pe.Code)
		}
	}
}

func (f *fieldErrors) locale(field, v string) {
	if !slices.Contains(i18n.Default().Locales(), v) {
		f.add(field, CodeInvalidValue)
	}
}

func (f *fieldErrors) timezone(field, v string) {
	if v == "" || v == "Local" {
		f.add(field, CodeInvalidValue)
		return
	}
	if _, err := time.LoadLocation(v); err != nil {
		f.add(field, CodeInvalidValue)
	}
}

func (f *fieldErrors) theme(field, v string) {
	if v != "system" && v != "light" && v != "dark" {
		f.add(field, CodeInvalidValue)
	}
}

func (f *fieldErrors) role(field string, r store.Role) {
	if !r.Valid() {
		f.add(field, CodeInvalidValue)
	}
}
