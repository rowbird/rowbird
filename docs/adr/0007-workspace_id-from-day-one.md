# ADR-0007: workspace_id from day one

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Teams/workspaces are an enterprise feature, but retrofitting tenancy later means migrating every table and query.

## Decision

Every tenant-owned table has workspace_id; the store layer enforces scoping. OSS runs one hidden default workspace.

## Consequences

Slight overhead now; enabling workspaces later is a feature flag, not a migration. Prevents IDOR by design.
