# 01: Architecture

## Big picture

```
                         ┌──────────────────── rowbird (single Go binary) ────────────────────┐
  Browser ──────────────►│  Vue SPA (embedded)      REST API /api/v1 (OpenAPI)   SSE stream   │
  CLI / scripts ────────►│                                │                                   │
                         │   ┌────────────┐   ┌─────────┐ │ ┌───────────┐   ┌──────────────┐  │
                         │   │ Scheduler  │──►│ Queue   │─┼►│ Runner    │──►│ Condition    │  │
                         │   │ (polls DB) │   │ (DB)    │ │ │ workers   │   │ + Formatters │  │
                         │   └────────────┘   └─────────┘ │ └─────┬─────┘   └──────┬───────┘  │
                         │                                │       │                │          │
                         │   Store (SQLite | Postgres) ◄──┴───────┘      Artifact storage     │
                         │   Notifications · Metrics · Health · AI assistant (optional)       │
                         └─────────┬──────────────────────────────────────────────┬───────────┘
                                   ▼                                              ▼
                         User databases (read-only)                 Destinations (email, Slack, ...)
```

## Components

| Component | Responsibility |
|---|---|
| **API** (`internal/api`) | Implements generated OpenAPI interfaces; auth middleware; SSE endpoint. |
| **Store** (`internal/store`) | Repositories, migrations, **workspace scoping**, optimistic concurrency. |
| **Scheduler** | Every `ROWBIRD_SCHEDULER_TICK` (default 5s): claim due reports, create `Run(pending)`, advance `next_run_at` atomically. |
| **Runner** | Worker pool (`ROWBIRD_WORKERS`, default 4) consuming pending runs; executes the run lifecycle (03-flows). |
| **Plugin registry** | Maps plugin ids → implementations for each plugin kind; exposes schemas to the API. |
| **Artifact storage** | Stores generated files; serves shared links. |
| **Notify** | In-app notifications, system alerts with fallback, grouping, heartbeat. |
| **GitOps** | YAML export/import/apply/diff. |
| **AI** | Builds prompts from request + schema, calls provider, returns structured proposal. |

## Plugin system (ADR-0004)

Plugin kinds: `connector`, `formatter`, `condition`, `destination`, `ai_provider`. Artifact storage
backends (`local`, `s3`) share a fixed contract too, but one is chosen for the instance by
configuration, so they are not plugins (ADR-0023).

- Plugins are **compiled in** and self-register in `init()` via `plugin.Register(kind, id, factory)`;
  `internal/app/plugins.go` lists them with blank imports.
- Each plugin exposes **Metadata** (id, name, icon, description, version), a **ConfigSchema**
  (JSON Schema subset, secret fields marked with `x-secret: true`), **Capabilities** and its own
  **translations** (embedded `locales/<lang>.json`), so adding a plugin needs no frontend change
  (ADR-0018).
- The core depends only on the contracts in `internal/plugin`. See 04-plugins for interfaces.
- The API exposes `GET /api/v1/plugins` so the UI renders configuration forms automatically.
- Every plugin kind has a **conformance test suite** that each implementation must pass.

## Process model

- One process serves API + UI + scheduler + workers. Flags allow disabling roles
  (`--no-scheduler`, `--no-workers`) for future split deployments.
- The queue is the `runs` table (status `pending`). Claiming uses an atomic update
  (`UPDATE ... WHERE status='pending' ... RETURNING` on SQLite; `FOR UPDATE SKIP LOCKED` on Postgres).
- With Postgres, N instances can run concurrently without leader election (ADR-0009).
- Graceful shutdown: stop claiming, wait for running runs up to a timeout, then mark them
  `cancelled` with reason `shutdown` (they are **not** silently lost).

## Frontend embedding (ADR-0002)

`web/` builds to `web/dist`, embedded with `//go:embed`. The server serves the SPA for non-API paths
(history fallback). The frontend never depends on server-side rendering; it can be replaced by
another SPA consuming the same API.

## Enterprise code

`ee/` holds commercially licensed code; it is empty in v1. The OSS build must compile and pass all
tests without `ee/`. Enterprise features hook in via interfaces (e.g. `authz.Policy`).

## Store access and migrations

- The internal store is accessed through bun, with workspace scoping enforced by a scoped query
  builder inside `internal/store` (ADR-0015).
- Migrations use goose with one embedded migration set per dialect, a database lock and an automatic
  SQLite backup before migrating (ADR-0016).
