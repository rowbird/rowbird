# ADR-0016: goose for migrations

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The store keeps a separate migration set per dialect (ADR-0003). Migrations run automatically at
startup and must be guarded by a database lock so that several instances starting together on
PostgreSQL do not race (08-operations). The SQLite store must also be backed up before migrating.
We compared goose and golang-migrate.

golang-migrate uses separate up and down files, leaves the database in a "dirty" state that needs
manual intervention after a failed migration, has no Go migrations and needs a separate driver
wrapper for `modernc.org/sqlite`.

## Decision

Use goose v3 as a library through its `Provider` API.

- Migrations are embedded with `embed.FS` from `internal/store/migrations/sqlite` and
  `internal/store/migrations/postgres`, one SQL file per version with up and down sections. Go
  migrations are allowed when SQL alone is not enough (for example data backfills that need
  application code).
- PostgreSQL uses goose's session locker, which holds an advisory lock for the duration of the run.
- goose ships no locker for SQLite, so Rowbird implements one on a `rowbird_migration_lock` table:
  the lock is taken with an atomic insert and a lock older than a timeout is considered stale and
  replaced. SQLite is a single-instance deployment, so this mainly protects against two local
  processes (for example `rowbird migrate` while a server starts).
- Before applying pending migrations to a SQLite store, Rowbird writes a consistent copy with
  `VACUUM INTO` next to the database file. Nothing is written when there is nothing to apply.
- There are no automatic downgrades. Down sections exist for development only.

## Consequences

Adding a migration means adding one file to each dialect directory with the same version number.
A test runs all migrations from scratch on both dialects, and the health check reports whether the
store is at the latest embedded version. We own a small amount of locking code for SQLite.
