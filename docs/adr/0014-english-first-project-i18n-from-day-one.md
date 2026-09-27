# ADR-0014: English-first project, i18n from day one

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The audience is international; the maintainer and early users are Brazilian.

## Decision

Code, docs, README and API in English. UI and messages are fully internationalized, shipping en and pt-BR. Locales are plain JSON files; CI checks key completeness.

## Consequences

Adding a language needs no code changes.
