# ADR-0010: Server-Sent Events for real time

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The UI needs live run status and notifications; traffic is server→client only.

## Decision

Use SSE at GET /api/v1/events.

## Consequences

Simpler than WebSockets, works through proxies with buffering disabled (documented).
