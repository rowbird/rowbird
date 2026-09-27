# Concepts

## Connections

A connection holds what Rowbird needs to reach one of your databases: PostgreSQL, MySQL and
MariaDB, SQL Server or SQLite. Network databases support TLS (from `disable` to `verify-full`) and
SSH tunnels with pinned host keys. Passwords and keys are encrypted with the master key and never
shown again.

Each connection has a query timeout, a row limit and a switch for multi-statement queries (off by
default). Queries run in read-only transactions where the database supports it. Use a login that
can only read; Rowbird checks and warns when it can write. Only admins manage connections.

## Queries

A query is named, versioned SQL bound to a connection. Every save creates a version; you can diff
two versions and restore an old one.

Parameters are written <code v-pre>{{name}}</code> and are always sent to the database as bound parameters, never
pasted into the SQL. Built-in parameters are resolved in the report's time zone:

| Parameter | Value |
|---|---|
| <code v-pre>{{now}}</code> | the current date and time |
| <code v-pre>{{today}}</code>, <code v-pre>{{yesterday}}</code> | dates |
| <code v-pre>{{start_of_week}}</code> | Monday of this week |
| <code v-pre>{{start_of_month}}</code>, <code v-pre>{{start_of_last_month}}</code>, <code v-pre>{{end_of_last_month}}</code> | dates |
| <code v-pre>{{start_of_year}}</code> | January 1st |

Your own parameters have a type (`text`, `integer`, `decimal`, `boolean`, `date`, `datetime`) and an
optional default that each report can override.

## Reports

A report runs a query on a schedule: a cron expression and a time zone. Occurrences follow the
wall clock of that zone, so "every day at 07:00" stays at 07:00 across daylight saving changes.
The schedule builder describes the expression in words and lists the next runs.

A report also sets what happens when things go wrong:

- **Retries** with a backoff, for failed queries.
- **Misfire policy**: what to do with runs missed while Rowbird was down (`skip` or `run_once`).
- **Overlap policy**: what to do when a run is still going when the next is due (`skip` or `queue`).
- **Auto-pause** after a number of failures in a row, with a notification to the owner.

## Conditions

A condition decides whether a run delivers. Rules can be combined with `all` or `any`:

| Rule | Passes when |
|---|---|
| `always` | always (the default) |
| `has_rows`, `is_empty` | the result has rows, or has none |
| `row_count` | the number of rows compares (`eq`, `ne`, `gt`, `gte`, `lt`, `lte`) with a value |
| `value` | a column of the first row compares with a value (or is `between` two) |
| `changed` | the result differs from the last delivered one |

A run whose condition does not pass is **skipped**. Destinations that track status, such as
Uptime Kuma, still hear about it.

## Channels and deliveries

A channel is a configured destination: "Sales Slack", "Finance mailing list". A delivery connects a
report to a channel with a mode and formats:

- **inline**: the rows in the message itself (an HTML table in email, Markdown in Slack and
  Discord, formatted text in Telegram).
- **attachment**: files (CSV, Excel, JSON, PDF) attached to the message. A file larger than the
  destination allows falls back to a link.
- **link**: a download link hosted by Rowbird, with an expiry, optional sign-in, revocation and a
  download log. Links need `ROWBIRD_BASE_URL`.

Messages can be customized with templates (subject, intro, message) that use variables such as
<code v-pre>{{report.title}}</code> and <code v-pre>{{run.rows}}</code>; values are escaped for each destination.

## Runs

Every execution is a run: scheduled, manual, or a test. A run records its status, the query version
and parameters it used, the condition's outcome, a sample of the result and each delivery attempt.
Failed deliveries can be resent with the files of that run. The full result can be downloaded in any
format for 24 hours after the run.

## Alerts and notifications

Admins pick an alert channel (and a fallback) for system problems: failing reports, failing
channels, auto-paused reports, failed backups. Alerts are grouped and followed by a recovery
message. The same events appear in the in-app notification center, and an optional heartbeat URL
(Uptime Kuma push, Healthchecks.io) is called while Rowbird is healthy.

## Users and roles

| Role | Can |
|---|---|
| viewer | see everything except secrets, download files, view runs |
| editor | viewer + create and edit queries, reports and deliveries, run reports, use the AI assistant |
| admin | editor + connections, channels, users, API keys, settings and imports |

See [Sign-in](/security/authentication) for passwords, 2FA, passkeys, OIDC and API keys.

## AI assistant

Optional and off by default. When an admin configures a provider (OpenAI, Anthropic, Gemini,
Ollama or any OpenAI-compatible endpoint), editors can ask for SQL or a schedule in plain words.
Only the request and the database schema are sent, never rows, and tables can be excluded. The
answer is a proposal: nothing runs until you apply it.
