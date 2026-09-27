# ADR-0003: SQLite by default, PostgreSQL optional

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Self-hosters want zero dependencies; larger teams want HA.

## Decision

Internal store defaults to SQLite via modernc.org/sqlite (pure Go, no CGO). PostgreSQL is supported for multi-instance deployments. Migrations are maintained per dialect.

## Consequences

Every repository is tested on both dialects. SQL must stay portable or be dialect-specific behind the repository layer.
