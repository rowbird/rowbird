# Roadmap

What Rowbird does today and where it is going. Ideas and priorities change with feedback: open an
issue or a discussion to argue for something.

## v1.0 (first public release)

- **Databases:** PostgreSQL, MySQL and MariaDB, SQL Server, SQLite, with TLS and SSH tunnels;
  read-only transactions, timeouts, row limits, bound parameters.
- **Queries:** SQL editor with schema autocomplete, preview, built-in date parameters, versions.
- **Reports:** cron schedules with time zones, conditions, retries, misfire and overlap policies,
  auto-pause, manual runs and test deliveries.
- **Formats and delivery:** CSV, Excel, JSON, PDF, inline tables; email, Slack, Telegram, Discord,
  webhooks, Uptime Kuma, S3; inline, attachment and expiring download links.
- **Alerting:** alert channels with fallback, grouping and recovery, heartbeat, notification center.
- **Access:** roles, TOTP and passkeys, OIDC, scoped API keys, security log.
- **Config as code:** YAML export and import, `rowbird apply`, GitOps directory.
- **AI assistant (optional):** SQL and schedule proposals from OpenAI, Anthropic, Gemini, Ollama or
  any OpenAI-compatible endpoint.
- **Operations:** single binary and distroless image, SQLite or PostgreSQL, several instances,
  backups, key rotation, health and Prometheus metrics. English and Brazilian Portuguese.

## Next

- [ ] Recipes gallery (docs.rowbird.dev/recipes): importable YAML reports for sources with a known
  schema (Stripe synced to Postgres, WooCommerce, Magento, Odoo, Chatwoot...), such as CAC, LTV by
  cohort and churn alerts; generic recipes whose SQL the AI assistant adapts to your schema
- [ ] Documentation in Brazilian Portuguese
- [ ] More connectors: ClickHouse, Oracle, Snowflake, BigQuery, DuckDB
- [ ] More destinations: Microsoft Teams, Google Chat, Mattermost, ntfy, SFTP
- [ ] More UI languages
- [ ] A Grafana dashboard for the Prometheus metrics

## Not planned

Dashboards and chart builders, moving data between databases, and writing to your databases.
Rowbird stays a focused tool for getting SQL results to people.
