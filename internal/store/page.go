package store

import (
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Page limits for cursor pagination (docs/spec/05-api.md).
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// Page is one page of a list, newest first. NextCursor is empty on the last page.
type Page[T any] struct {
	Items      []T
	NextCursor string
}

// PageRequest selects a page. A zero value means the first page with the default limit.
type PageRequest struct {
	Cursor string
	Limit  int
}

// ErrInvalidCursor is returned for a cursor that was not produced by this package.
var ErrInvalidCursor = fmt.Errorf("store: invalid cursor")

func (p PageRequest) limit() int {
	switch {
	case p.Limit <= 0:
		return DefaultPageLimit
	case p.Limit > MaxPageLimit:
		return MaxPageLimit
	}
	return p.Limit
}

// encodeCursor hides the id behind base64 so clients treat cursors as opaque.
func encodeCursor(id uuid.UUID) string { return base64.RawURLEncoding.EncodeToString(id[:]) }

func decodeCursor(s string) (uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 16 {
		return uuid.Nil, ErrInvalidCursor
	}
	return uuid.UUID(b), nil
}

// paginate orders q by id, newest first (ids are UUIDv7, so time ordered) and applies the cursor.
// It fetches one extra row to know whether a next page exists; finishPage trims it.
func paginate(q *bun.SelectQuery, req PageRequest) (*bun.SelectQuery, error) {
	if req.Cursor != "" {
		id, err := decodeCursor(req.Cursor)
		if err != nil {
			return nil, err
		}
		q = q.Where("?TableAlias.id < ?", id)
	}
	return q.OrderExpr("?TableAlias.id DESC").Limit(req.limit() + 1), nil
}

func finishPage[T any](items []T, req PageRequest, id func(T) uuid.UUID) Page[T] {
	limit := req.limit()
	if len(items) <= limit {
		return Page[T]{Items: items}
	}
	items = items[:limit]
	return Page[T]{Items: items, NextCursor: encodeCursor(id(items[limit-1]))}
}
