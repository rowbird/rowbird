# ADR-0026: Configuration as code through the API's services, with GitOps ownership

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

Phase 9 adds one YAML format for UI export and import, `rowbird apply`, a GitOps directory and
templates (docs/spec/09-config-as-code.md). Imports must follow the same rules as the API, never
leak secrets, be all or nothing, show a plan before changing anything, and round-trip exactly
between instances. Resources that come from Git must not drift through the UI, but people need a
way out when the directory cannot be changed right away.

## Decision

- **One package, the API's services.** `internal/gitops` parses (go.yaml.in/yaml/v3 with node
  positions, unknown fields as errors), exports, plans and applies. Applying calls the same services
  as the API (connections, channels, queries, reports and deliveries), so validation, secrets
  handling (ADR-0022), security events and scheduling behave the same. The export reuses the same
  code to describe what exists, so the plan compares documents in one shape.
- **One transaction, a dry run that really applies.** The services join an outer transaction
  (`store.RunInTx` nests). A dry run applies and rolls back, so it finds every error a real apply
  would. Connection checks, which reach the user's database, run after the commit.
- **Names, not ids; queries as documents.** References are by name; exports write queries as
  their own documents so title and description survive; reports may still hold inline queries.
- **Patch semantics with declared lists.** Omitted fields keep their value, except the ones that are
  replaced as a whole (configurations, a report's condition, parameter overrides and deliveries),
  which a document declares; deliveries are replaced only when they change.
- **Secrets as environment placeholders.** Exports write `${env:ROWBIRD_<KIND>_<NAME>_<FIELD>}`;
  imports take values from the user or the environment, keep stored ones on update, and compare
  them without showing them.
- **Ownership.** `managed_by` records `gitops` for what the directory applied (the API refuses
  changes) and `gitops_detached` for what an admin took over (the directory skips it). Reports
  that leave the directory are paused as orphans and notified, never deleted.

## Consequences

- An import cannot bypass a rule the API enforces, and new rules apply to it automatically.
- Large imports hold one write transaction; on SQLite that briefly blocks other writers.
- Removing a report from Git pauses it instead of deleting it: deletion stays a deliberate admin
  action in the UI.
- Deliveries replaced by an import lose the link to earlier attempts (resends of past runs).
