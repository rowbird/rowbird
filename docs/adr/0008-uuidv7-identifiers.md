# ADR-0008: UUIDv7 identifiers

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

IDs appear in URLs, YAML-free references and logs; sequential ints leak volume and complicate multi-instance.

## Decision

All primary keys are UUIDv7 (time-ordered).

## Consequences

Good index locality and cursor pagination; not guessable; generated in the app.
