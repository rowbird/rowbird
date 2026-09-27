# Introduction

Rowbird runs SQL queries on a schedule and delivers the results, formatted, to the places your team
already reads: email, Slack, Telegram, Discord, webhooks, Uptime Kuma and S3-compatible storage.
Add a condition ("only when there are rows", "when the value is above 100", "when the result
changed") and the same report becomes a data alert.

It is built for developers, analysts and small teams that already have a production database and
today solve recurring reports with cron and scripts, or with a BI tool that is too heavy for the job.

## What Rowbird is not

- **Not a BI tool.** There is no chart builder and no dashboards. The result of a query goes to
  people, formatted.
- **Not an ETL tool.** It does not move or transform data between databases.
- **Not an uptime monitor.** It integrates with one (Uptime Kuma, Healthchecks) instead.
- **Never a writer.** Rowbird reads your databases and never writes to them.

## How it works

1. An admin adds a **connection** to a database and a **channel** for each destination (the sales
   Slack channel, the finance mailing list).
2. An editor writes a **query**, previews it, and saves it. Queries are versioned.
3. A **report** puts the query on a schedule, with an optional condition and one or more
   **deliveries**: which channel, in which mode (inline, attachment or link) and in which formats.
4. Each execution is a **run**. You can see its rows, the condition's outcome, every delivery
   attempt, and resend or download its files.

Read [Concepts](./concepts) for the details, or go straight to the [quick start](./quick-start).

## Principles

- **Read-only and safe by default.** Read-only transactions where the database supports them,
  server-side timeouts, row limits, one statement per query, parameters always bound.
- **Preview before acting.** Query results, schedules, messages and imports can all be previewed.
- **One command to install, one process to run.**
- **API first.** The web UI is one client of the [HTTP API](/reference/api).
- **No telemetry.** The only outbound call Rowbird makes on its own is an optional daily update
  check, which you can turn off.
