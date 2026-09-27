// Package apperr defines domain errors that services return and the API maps to HTTP responses.
// Services never know about HTTP; they pick a Kind and a stable code (docs/spec/05-api.md).
package apperr

import (
	"errors"
	"time"
)

// Kind classifies an error. The API maps each kind to one HTTP status.
type Kind int

// Kinds of domain errors.
const (
	KindInvalid         Kind = iota + 1 // 400
	KindUnauthenticated                 // 401
	KindForbidden                       // 403
	KindNotFound                        // 404
	KindConflict                        // 409
	KindTooManyRequests                 // 429
	KindUnprocessable                   // 422, the request is valid but the operation failed (for example a database refused the connection)
	KindGone                            // 410, the resource existed but is no longer available
)

// FieldError points at one invalid input field.
type FieldError struct {
	Field string
	Code  string
}

// Dependent is something that uses a resource and blocks its deletion.
type Dependent struct {
	Type string
	ID   string
	Name string
}

// Error is a domain error with a stable code such as "auth.invalid_credentials".
type Error struct {
	Kind       Kind
	Code       string
	Fields     []FieldError
	RetryAfter time.Duration
	// Dependents are returned with "in use" conflicts (docs/spec/02-data-model.md).
	Dependents []Dependent
}

func (e *Error) Error() string { return e.Code }

// Is makes errors.Is match on the code, so sentinel values can be compared after wrapping.
func (e *Error) Is(target error) bool {
	var t *Error
	return errors.As(target, &t) && t.Code == e.Code
}

// New returns an error of the given kind and code.
func New(kind Kind, code string) *Error { return &Error{Kind: kind, Code: code} }

// Invalid returns a validation error listing the offending fields.
func Invalid(fields ...FieldError) *Error {
	return &Error{Kind: KindInvalid, Code: "validation.failed", Fields: fields}
}

// Field is a shorthand for a FieldError.
func Field(field, code string) FieldError { return FieldError{Field: field, Code: code} }

// As extracts an *Error from err.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}
