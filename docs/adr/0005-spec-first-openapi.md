# ADR-0005: Spec-first OpenAPI

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Backend, frontend and docs must not drift; AI-assisted implementation benefits from an explicit contract.

## Decision

api/openapi.yaml is the source of truth. Go strict-server interfaces (oapi-codegen) and the TS client (openapi-typescript + openapi-fetch) are generated. CI fails on generated-code drift.

## Consequences

API changes always start in the YAML. Generated code is never edited by hand.
