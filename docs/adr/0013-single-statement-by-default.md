# ADR-0013: Single statement by default

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Multiple statements increase risk (e.g. injected DDL) but some real reports (notably SQL Server) need temp tables.

## Decision

Queries are single-statement by default; a per-connection allow_multi_statement flag (admin only, with a clear warning) enables multiple statements.

## Consequences

Safe default without blocking legitimate use cases.
