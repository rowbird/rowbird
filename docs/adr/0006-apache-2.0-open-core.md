# ADR-0006: Apache-2.0 open core

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The project targets adoption (and the maintainer's CV) while keeping the door open for a paid cloud/enterprise edition.

## Decision

Core licensed Apache-2.0. Enterprise features (teams isolation, granular RBAC, SAML/SCIM, full audit log, column masking) live in ee/ under a commercial license. OIDC stays free.

## Consequences

OSS build must compile and pass tests without ee/. Enterprise hooks via interfaces.
