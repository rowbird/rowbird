# Architecture Decision Records

| ADR | Title | Status |
|---|---|---|
| [0001](0001-go-for-the-backend.md) | Go for the backend | Accepted |
| [0002](0002-vue-spa-api-first-embedded-in-the-binary.md) | Vue SPA, API-first, embedded in the binary | Accepted |
| [0003](0003-sqlite-by-default-postgresql-optional.md) | SQLite by default, PostgreSQL optional | Accepted |
| [0004](0004-compile-time-plugins.md) | Compile-time plugins | Accepted |
| [0005](0005-spec-first-openapi.md) | Spec-first OpenAPI | Accepted |
| [0006](0006-apache-2.0-open-core.md) | Apache-2.0 open core | Accepted |
| [0007](0007-workspace_id-from-day-one.md) | workspace_id from day one | Accepted |
| [0008](0008-uuidv7-identifiers.md) | UUIDv7 identifiers | Accepted |
| [0009](0009-scheduler-polls-next_run_at-in-the-database.md) | Scheduler polls next_run_at in the database | Accepted |
| [0010](0010-server-sent-events-for-real-time.md) | Server-Sent Events for real time | Accepted |
| [0011](0011-pure-go-pdf-generation.md) | Pure-Go PDF generation | Accepted |
| [0012](0012-no-telemetry.md) | No telemetry | Accepted |
| [0013](0013-single-statement-by-default.md) | Single statement by default | Accepted |
| [0014](0014-english-first-project-i18n-from-day-one.md) | English-first project, i18n from day one | Accepted |
| [0015](0015-bun-for-internal-store-access.md) | bun for internal store access | Accepted |
| [0016](0016-goose-for-migrations.md) | goose for migrations | Accepted |
| [0017](0017-authorization-declared-in-openapi.md) | Authorization declared in the OpenAPI document | Accepted |
| [0018](0018-plugin-schema-format-and-embedded-translations.md) | Plugin schema format and embedded translations | Accepted |
| [0019](0019-schedule-occurrences-computed-on-the-local-wall-clock.md) | Schedule occurrences computed on the local wall clock | Accepted |
| [0020](0020-run-queue-with-status-guarded-transitions.md) | Run queue with retries in the queue and status-guarded transitions | Accepted |
| [0021](0021-locale-aware-outputs-and-golden-files.md) | Locale-aware outputs, exact numbers and golden files | Accepted |
| [0022](0022-shared-handling-of-plugin-configurations-with-secrets.md) | Shared handling of plugin configurations with secrets | Accepted |
| [0023](0023-artifacts-generated-once-with-link-fallback.md) | Artifacts generated once per run, link fallback and resends | Accepted |
| [0024](0024-grouped-notifications-alerts-with-fallback-and-in-process-events.md) | Grouped notifications, alerts with a fallback, and in-process events | Accepted |
| [0025](0025-ai-proposals-with-structured-output-and-a-schema-budget.md) | AI proposals with structured output, a schema budget and the assistant off by default | Accepted |
| [0026](0026-config-as-code-through-the-api-services.md) | Configuration as code through the API's services, with GitOps ownership | Accepted |
| [0027](0027-passkeys-and-oidc-sign-in.md) | Passkeys as a second factor of their own, and OIDC sign-in that owns 2FA | Accepted |
| [0028](0028-operations-backups-rotation-retention.md) | Backups configured by the operator, key rotation with a previous key, retention by lease | Accepted |
| [0029](0029-release-pipeline.md) | Releases with GoReleaser, a distroless non-root image and keyless signing | Accepted |

New decisions: copy `template.md`, next number, status `Proposed` → `Accepted`. Superseded ADRs are kept and marked `Superseded by ADR-XXXX`.
