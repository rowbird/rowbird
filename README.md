<h1 align="center">
  <img src="docs/assets/logo.svg" alt="Rowbird" width="300">
</h1>

<p align="center"><strong>Your SQL results, delivered.</strong></p>

<p align="center">
  Run SQL queries on a schedule and deliver the results, as CSV, Excel, PDF, JSON or a table in the
  message, to email, Slack, Telegram, Discord, webhooks, Uptime Kuma and S3. Add a condition and the
  same report becomes a data alert. Self-hosted, one binary, no telemetry.
</p>

<p align="center">
  <a href="https://docs.rowbird.dev">Documentation</a> ·
  <a href="https://docs.rowbird.dev/guide/demo">Demo</a> ·
  <a href="https://github.com/rowbird/rowbird/releases">Releases</a>
</p>

<p align="center"><img src="docs/assets/rowbird.gif" alt="Rowbird: from a query to an email in a minute" width="800"></p>

## Quick start

```bash
docker run -d --name rowbird -p 8080:8080 -v rowbird-data:/data \
  -e ROWBIRD_BASE_URL=http://localhost:8080 \
  ghcr.io/rowbird/rowbird:1
```

Open http://localhost:8080 and create the first admin. Then back up `/data/master.key`, the key
that encrypts your stored credentials.

**Try it with sample data**, a fake mail server and ready-made reports:

```bash
git clone https://github.com/rowbird/rowbird.git && cd rowbird/deploy/demo
docker compose up
```

**For production**, [`deploy/compose`](deploy/compose) runs Rowbird behind Caddy with HTTPS, a
master key kept as a secret and nightly backups. Binaries for Linux, macOS and Windows (amd64 and
arm64), a systemd unit and Kubernetes manifests are covered in the
[install guide](https://docs.rowbird.dev/install/docker).

## Features

- **Databases:** PostgreSQL, MySQL and MariaDB, SQL Server, SQLite, with TLS and SSH tunnels.
  Read-only transactions, server-side timeouts, row limits, bound parameters.
- **Queries:** SQL editor with schema autocomplete, preview, built-in date parameters
  (`{{yesterday}}`, `{{start_of_month}}`), versions with diffs.
- **Schedules:** cron with a time zone and a visual builder, misfire and overlap policies, retries,
  auto-pause after repeated failures, manual runs, "send test to me".
- **Conditions:** has rows, is empty, row count, value comparison, changed since last run, combined
  with all or any.
- **Formats:** CSV, Excel, JSON and PDF files; inline HTML tables, Markdown and text. Numbers and
  dates follow the reader's locale.
- **Delivery modes:** inline, attachment, or a download link with expiry, optional sign-in,
  revocation and a download log. Large attachments fall back to links.
- **Alerting:** alert channels with a fallback, grouping and recovery messages, a heartbeat for
  your uptime monitor, an in-app notification center.
- **Access:** admin, editor and viewer roles, TOTP and passkeys, OIDC single sign-on, scoped API
  keys, a security log.
- **Config as code:** YAML export and import with a plan and dry run, `rowbird apply`, a GitOps
  directory applied at startup.
- **AI assistant (optional):** OpenAI, Anthropic, Gemini, Ollama or any OpenAI-compatible endpoint
  proposes SQL and schedules. It sees the schema, never your rows, and nothing runs until you apply it.
- **Operations:** SQLite by default, PostgreSQL for several instances, backups and restore, master
  key rotation, `/health` and Prometheus `/metrics`, structured logs. English and Brazilian
  Portuguese.

| Databases | Destinations |
|---|---|
| PostgreSQL, MySQL, MariaDB, SQL Server, SQLite | Email (SMTP), Slack, Telegram, Discord, Webhook (HMAC-signed), Uptime Kuma, S3-compatible (AWS, MinIO, R2, B2, Wasabi, Spaces) |

<!-- TODO(launch): screenshots in docs/assets: query editor, report editor, run detail, the email. -->

## Why not Metabase or Redash for this?

They are great BI tools, and if you need dashboards and exploration, use them. Rowbird does one
job: a query, a schedule, a condition, and the result formatted where people will read it. It is a
single small binary with SQLite, so it fits next to the cron job it replaces, and it treats your
database as read only, keeps secrets encrypted and sends nothing anywhere you did not configure.

## What Rowbird is not

- **Not a BI tool:** no chart builder, no dashboards.
- **Not an ETL tool:** it does not move or transform data between databases.
- **Not an uptime monitor:** it integrates with one (Uptime Kuma, Healthchecks) instead.
- **Never a writer:** it reads your databases and never writes to them.

## Contributing

Bug reports, ideas and new connectors or destinations are welcome. Read
[CONTRIBUTING.md](CONTRIBUTING.md) and the
[plugin development guide](https://docs.rowbird.dev/plugins/development). Please report security
issues privately, as described in [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE).
