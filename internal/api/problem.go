package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/i18n"
	"github.com/rowbird/rowbird/internal/store"
)

// Stable error codes (docs/spec/05-api.md). The i18n key is "errors." + code.
const (
	CodeInternal         = "internal"
	CodeRequestInvalid   = "request.invalid"
	CodeMethodNotAllowed = "request.method_not_allowed"
	CodeRouteNotFound    = "route.not_found"
	CodeNotFound         = "resource.not_found"
	CodeConflictVersion  = "conflict.version"
	CodeConflictDup      = "conflict.duplicate"
	CodeValidation       = "validation.failed"
	CodeUnsupportedMedia = "request.unsupported_media_type"
	CodeUnauthenticated  = "auth.unauthenticated"
	CodeForbidden        = "auth.forbidden"
	CodeCSRFFailed       = "auth.csrf_failed"
	CodeRateLimited      = "auth.rate_limited"
)

const problemTypeBase = "https://rowbird.dev/errors/"

// Error is an error that carries an HTTP status and a stable code.
type Error struct {
	Status     int
	Code       string
	Detail     string
	Fields     []gen.FieldError
	RetryAfter time.Duration
	Dependents []gen.Dependent
	Err        error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Err.Error()
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

// toError maps any error to an *Error. Unknown errors become a 500 whose detail is never shown.
func toError(err error) *Error {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	if de, ok := apperr.As(err); ok {
		e := &Error{Status: kindStatus[de.Kind], Code: de.Code, RetryAfter: de.RetryAfter, Err: err}
		for _, d := range de.Dependents {
			if id, perr := uuid.Parse(d.ID); perr == nil {
				e.Dependents = append(e.Dependents, gen.Dependent{Type: d.Type, Id: id, Name: d.Name})
			}
		}
		if e.Status == 0 {
			e.Status = http.StatusInternalServerError
		}
		for _, f := range de.Fields {
			e.Fields = append(e.Fields, gen.FieldError{Field: f.Field, Code: f.Code})
		}
		return e
	}
	switch {
	case errors.Is(err, store.ErrInvalidCursor):
		return &Error{Status: http.StatusBadRequest, Code: CodeValidation, Fields: []gen.FieldError{{Field: "cursor", Code: "validation.invalid_value"}}, Err: err}
	case errors.Is(err, store.ErrNotFound):
		return &Error{Status: http.StatusNotFound, Code: CodeNotFound, Err: err}
	case errors.Is(err, store.ErrConflict):
		return &Error{Status: http.StatusConflict, Code: CodeConflictVersion, Err: err}
	case errors.Is(err, store.ErrDuplicate):
		return &Error{Status: http.StatusConflict, Code: CodeConflictDup, Err: err}
	}
	return &Error{Status: http.StatusInternalServerError, Code: CodeInternal, Err: err}
}

var kindStatus = map[apperr.Kind]int{
	apperr.KindInvalid:         http.StatusBadRequest,
	apperr.KindUnauthenticated: http.StatusUnauthorized,
	apperr.KindForbidden:       http.StatusForbidden,
	apperr.KindNotFound:        http.StatusNotFound,
	apperr.KindConflict:        http.StatusConflict,
	apperr.KindTooManyRequests: http.StatusTooManyRequests,
	apperr.KindUnprocessable:   http.StatusUnprocessableEntity,
	apperr.KindGone:            http.StatusGone,
}

// problemWriter renders errors as RFC 9457 problem details, localized from Accept-Language.
type problemWriter struct {
	bundle *i18n.Bundle
	logger *slog.Logger
}

// writeError logs server-side failures and writes the problem response. Internal error messages
// stay in the logs; clients only see the stable code and a generic title.
func (p *problemWriter) writeError(w http.ResponseWriter, r *http.Request, err error) {
	e := toError(err)
	if e.Status >= http.StatusInternalServerError {
		p.logger.ErrorContext(r.Context(), "request failed", "error", err, "code", e.Code)
	}
	p.write(w, r, e)
}

func (p *problemWriter) write(w http.ResponseWriter, r *http.Request, e *Error) {
	locale := p.bundle.Match(r.Header.Get("Accept-Language"))
	key := "errors." + e.Code
	problem := gen.Problem{
		Type:    problemTypeBase + e.Code,
		Title:   p.bundle.T(locale, key),
		Status:  e.Status,
		Code:    e.Code,
		I18nKey: key,
	}
	instance := r.URL.Path
	problem.Instance = &instance
	if e.Detail != "" {
		problem.Detail = &e.Detail
	}
	if len(e.Fields) > 0 {
		problem.Errors = &e.Fields
	}
	if len(e.Dependents) > 0 {
		problem.Dependents = &e.Dependents
	}
	if id := RequestIDFrom(r.Context()); id != "" {
		problem.RequestId = &id
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(e.RetryAfter.Seconds()))))
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Content-Language", locale)
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(problem)
}
