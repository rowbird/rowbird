---
layout: home

hero:
  name: Rowbird
  text: Your SQL results, delivered.
  tagline: A self-hosted service that runs SQL queries on a schedule and delivers the results to email, Slack, Telegram, Discord, webhooks, Uptime Kuma and S3. Conditions turn reports into data alerts.
  actions:
    - theme: brand
      text: Quick start
      link: /guide/quick-start
    - theme: alt
      text: Try the demo
      link: /guide/demo
    - theme: alt
      text: GitHub
      link: https://github.com/rowbird/rowbird

features:
  - title: Your database, read only
    details: PostgreSQL, MySQL and MariaDB, SQL Server and SQLite, with TLS and SSH tunnels. Read-only transactions, server-side timeouts, row limits and bound parameters.
  - title: Formats people can use
    details: CSV, Excel, JSON and PDF files, or a table right in the message. Numbers and dates follow the reader's locale.
  - title: Where your team already is
    details: Email, Slack, Telegram, Discord, signed webhooks, Uptime Kuma and S3-compatible buckets. Large files fall back to expiring download links.
  - title: Alerts, not just reports
    details: Send only when there are rows, when a value crosses a threshold or when the result changed. Failures reach an alert channel, with recovery messages.
  - title: Config as code
    details: Export reports as YAML, apply them with the CLI, or point Rowbird at a Git-managed directory.
  - title: One binary, no telemetry
    details: A single Go binary or a small distroless image, SQLite by default, PostgreSQL for several instances. Rowbird only talks to what you configure.
---
