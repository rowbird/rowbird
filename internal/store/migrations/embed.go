// Package migrations embeds the SQL migrations of the internal store, one directory per dialect.
// Every version must exist in both directories (ADR-0016).
package migrations

import "embed"

// FS holds sqlite/*.sql and postgres/*.sql.
//
//go:embed sqlite/*.sql postgres/*.sql
var FS embed.FS
