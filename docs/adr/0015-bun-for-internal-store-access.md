# ADR-0015: bun for internal store access

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The internal store runs on SQLite by default and on PostgreSQL for multi-instance deployments
(ADR-0003). Every tenant-owned row carries `workspace_id`, and scoping must be enforced centrally in
the store layer so that handlers cannot bypass it (ADR-0007). We considered three options:

- **sqlc, one set of queries per dialect.** Type-safe and explicit, but every query is written twice,
  the two generated packages have different types that need an adapter layer, and the
  `workspace_id` filter lives in each `.sql` file with no single place that guarantees it.
- **database/sql plus a small query builder** (squirrel, goqu or our own). Light and fully under our
  control, but it brings a lot of scanning boilerplate, squirrel is in maintenance mode, and central
  scoping would have to be built from scratch anyway.
- **bun** (uptrace/bun). A SQL-first query builder with struct mapping and dialects for SQLite and
  PostgreSQL that runs on top of `database/sql`, so it works with `modernc.org/sqlite` and
  `pgx/v5/stdlib`.

## Decision

Use bun as the store access library, with `sqlitedialect` and `pgdialect`.

- The raw `*bun.DB` never leaves `internal/store`. A depguard rule in `.golangci.yml` forbids
  importing bun (and the internal database drivers) anywhere else.
- Repositories build tenant queries only through a `store.Scoped` value obtained from the request
  context. Its select, update and delete builders always carry `workspace_id = ?`, and its insert
  builder sets `workspace_id` on the model. A context without a workspace is an error, not a global
  query.
- An architecture test inspects the migrated schema and fails if a table outside a short allowlist of
  global tables lacks a `workspace_id` column.
- SQL that genuinely differs between dialects (for example claiming runs with `FOR UPDATE SKIP LOCKED`
  on PostgreSQL) stays inside the repository behind a switch on the dialect.
- We use bun as a query builder and mapper. Relations, soft deletes and model hooks that hide SQL are
  avoided so queries remain readable and predictable.

## Consequences

One Go implementation per repository serves both dialects, and scoping is enforced in one place that
tests can target directly. We depend on a larger library than a hand-rolled builder, and contributors
must learn bun's builder API. Schema changes still go through per-dialect migrations (ADR-0016), not
through bun's model-based table creation.
