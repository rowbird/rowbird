package store

import (
	"time"

	"github.com/google/uuid"
)

// Base holds the fields every entity has (docs/spec/02-data-model.md, "Common fields").
type Base struct {
	ID        uuid.UUID  `bun:"id,pk,type:uuid"`
	CreatedAt time.Time  `bun:"created_at,notnull"`
	UpdatedAt time.Time  `bun:"updated_at,notnull"`
	CreatedBy *uuid.UUID `bun:"created_by,type:uuid"`
	Version   int64      `bun:"version,notnull"`
}

func (b *Base) base() *Base { return b }

// TenantBase is Base plus workspace ownership. Every tenant-owned model embeds it, and Scoped
// fills WorkspaceID on insert.
type TenantBase struct {
	Base
	WorkspaceID uuid.UUID `bun:"workspace_id,notnull,type:uuid"`
}

func (t *TenantBase) tenant() *TenantBase { return t }

// versioned is satisfied by any model embedding Base.
type versioned interface{ base() *Base }

// tenantOwned is satisfied by any model embedding TenantBase.
type tenantOwned interface {
	versioned
	tenant() *TenantBase
}

// now is the store clock: UTC, truncated to microseconds so values survive a round trip through
// both dialects unchanged.
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
