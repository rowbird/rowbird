# ADR-0002: Vue SPA, API-first, embedded in the binary

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The maintainer knows Vue; the frontend should remain replaceable (e.g. React later) and the API must serve CLI/automation too.

## Decision

The UI is a Vue 3 + TypeScript SPA that talks only to the public REST API through a generated client. Built assets are embedded with go:embed. No server-side rendering or backend/frontend coupling (no Inertia-style bridges).

## Consequences

Frontend can be swapped by rewriting components only; API is complete by construction; one artifact to ship.
