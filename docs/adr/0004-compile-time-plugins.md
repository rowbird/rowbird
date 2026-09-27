# ADR-0004: Compile-time plugins

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Everything that varies (connectors, formats, conditions, destinations, AI providers, storage) must be easy to add. Go's runtime plugin package is fragile (no Windows, exact toolchain match).

## Decision

Plugins are Go packages compiled into the binary, registering themselves in a registry at init. Each declares metadata, a config schema and capabilities, and must pass a conformance suite. The generic webhook destination covers custom integrations without code.

## Consequences

Adding a plugin = PR; the UI renders forms from schemas so no frontend change is needed.
