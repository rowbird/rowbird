# 00: Overview

## One-liner

**Rowbird: your SQL results, delivered.** Schedule SQL queries and deliver the results where your
team already is.

## What Rowbird is

A self-hosted service that executes SQL queries on a schedule and delivers the results, formatted,
to channels such as email, Telegram, Slack, Discord, webhooks, Uptime Kuma and S3-compatible
storage. Conditions ("only send if there are rows", "if value > X", "if the result changed") let the
same machinery work as **data alerts**.

## What Rowbird is NOT (state this in the README)

- **Not a BI tool**: no drag-and-drop chart builder, no complex dashboards.
- **Not an ETL tool**: it does not move or transform data between databases.
- **Not an uptime monitor**: it integrates with them (Uptime Kuma, Healthchecks) instead.
- **Never writes to the user's database**: read-only by principle.

## Audience

Developers, analysts and small/medium teams that already have a production database and today solve
recurring reports with cron + scripts, or with Metabase/Redash, which are too heavy for this job.

## v1 scope (everything below ships in the first public release)

- **Databases:** PostgreSQL, MySQL/MariaDB, SQL Server, SQLite, with SSL/TLS and SSH tunnel.
- **Queries:** editor with schema autocomplete, preview, built-in date parameters, versions + diff.
- **AI assistant (optional):** OpenAI, Anthropic, Gemini, Ollama, any OpenAI-compatible endpoint.
  Receives only the request + schema (never rows); proposes SQL/cron, user approves.
- **Scheduling:** cron + timezone per report, visual builder, next-5-runs preview, misfire and overlap
  policies, retries, auto-pause after repeated failures, manual runs, test delivery.
- **Conditions:** always, has rows, is empty, row count, value comparison, changed since last run;
  combinable with all/any.
- **Formats:** CSV, XLSX, JSON, PDF as files; inline HTML (email), Markdown (Slack/Discord),
  formatted text (Telegram). Locale-aware number/date formatting.
- **Delivery modes:** inline, attachment, **link** (server-hosted link with configurable expiry,
  optional login, revocation, download log). Automatic fallback from attachment to link when a file
  exceeds a destination limit.
- **Destinations:** Email (SMTP), Telegram, Slack, Discord, generic Webhook (HMAC-signed),
  Uptime Kuma (push), S3-compatible (AWS, MinIO, R2, B2, Wasabi, Spaces).
- **Artifact storage:** local disk (default) or S3-compatible, with retention.
- **Notifications:** in-app notification center + health banner + per-channel health, system alert
  channel with fallback, alert grouping and recovery messages, outbound heartbeat.
- **Users & access:** users, roles (admin/editor/viewer), password + TOTP 2FA + passkeys, OIDC login,
  API keys with scopes, security event log.
- **Config as code:** YAML export/import (with mapping + conflict resolution + dry-run diff),
  `rowbird apply`, GitOps directory applied at startup.
- **Operations:** single binary / Docker image (amd64, arm64), SQLite or Postgres internal store,
  multi-instance with Postgres, backup/restore, `/health`, `/metrics` (Prometheus), structured logs.
- **i18n:** English and Brazilian Portuguese from day one; adding a language = adding files.

## Out of v1

WhatsApp (Meta Business API onboarding burden), NoSQL databases, dashboards, Helm chart
(plain k8s manifests example only), chained destinations beyond "link" mode.

## Editions (open core)

| Open source (Apache-2.0) | Enterprise / Cloud (commercial, `ee/`) |
|---|---|
| Everything in v1 scope above | Teams/workspaces with isolation |
| Global roles admin/editor/viewer | Granular per-resource permissions |
| Password, TOTP, passkeys, **OIDC** | SAML, SCIM provisioning |
| Security event log | Full exportable audit log |
| | Column masking per role |

The OSS edition runs with a single, invisible default workspace. The data model is multi-workspace
from day one (ADR-0007).

## Principles

1. **Everything that varies is a plugin** with a fixed contract: connectors, formats, conditions,
   destinations, AI providers, languages. Adding one = new package + registration + conformance
   tests. The core never knows concrete implementations. Artifact storage backends follow the same
   rule with a fixed contract, chosen by configuration instead of registration (ADR-0023).
2. **Read-only and safe by default.**
3. **Preview before acting:** query results, cron, message rendering, imports.
4. **Install in one command**, run as one process.
5. **API-first:** the UI is just one client of the public API.
6. **No telemetry, no surprises:** Rowbird only talks to what the user configured.

## Glossary

- **Connection**: credentials + options to reach a user database.
- **Query**: named, versioned SQL bound to a connection.
- **Report**: a schedule for a query, with conditions and deliveries.
- **Channel**: a configured destination instance (e.g. "Sales Slack"), reusable.
- **Delivery**: link between a report and a channel: format, mode, per-delivery overrides.
- **Run**: one execution of a report.
- **Delivery attempt**: the outcome of one delivery within a run.
- **Artifact**: a generated file (CSV, XLSX, PDF, JSON) stored in artifact storage.
- **Shared link**: a server-hosted, expiring link to an artifact.
- **Workspace**: tenant boundary; exactly one in the OSS edition.
