// Package ids generates identifiers. All primary keys are UUIDv7 (ADR-0008).
package ids

import (
	"fmt"

	"github.com/google/uuid"
)

// New returns a new time-ordered UUIDv7. It panics only if the system random source fails, which
// leaves the process unable to do anything safely anyway.
func New() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Sprintf("ids: generate UUIDv7: %v", err))
	}
	return id
}

// Parse parses the canonical string form of an id.
func Parse(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid id %q", s)
	}
	return id, nil
}
