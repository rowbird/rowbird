# ADR-0011: Pure-Go PDF generation

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Headless Chromium makes pretty PDFs but inflates the image by hundreds of MB and adds a process to manage.

## Decision

Generate PDFs with a pure-Go library (maroto v2) using a well-designed tabular report layout. An HTML→PDF plugin via an external service (e.g. Gotenberg) may come later.

## Consequences

Small image, simple deployment; less layout freedom.
