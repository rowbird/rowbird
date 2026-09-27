# 03: Flows

## 1. First run (setup wizard)

1. On start, if no user exists, the UI shows the setup wizard (API: `GET /api/v1/setup/status`).
2. If `ROWBIRD_MASTER_KEY(_FILE)` is not set, the server generates a key into
   `$ROWBIRD_DATA_DIR/master.key` (0600) and the wizard shows a **blocking warning**: back it up;
   without it encrypted credentials are unrecoverable.
3. Wizard: create admin (email, name, password), default language and timezone. The master key
   warning must be acknowledged before the form can be submitted. `GET /setup/status` reports where
   the key came from (`env`, `file` or `generated`, with the file path while setup is pending).
4. Submitting creates, in one transaction, the default workspace (slug `default`), the admin, its
   membership and the settings `default_locale`, `default_timezone` and `require_2fa=false`, then
   signs the admin in.
5. Setup endpoint is disabled forever once a user exists (`409 setup.completed`).
6. Until setup completes, anyone who reaches the UI can claim the instance. If
   `ROWBIRD_SETUP_TOKEN` is set, the wizard must send it; otherwise the server logs a warning at
   startup while setup is pending.

### Adding users

Invitations by email are not implemented yet (the system mailer channel exists, see 02), so an
admin adds a user with name, email and role and
receives a **temporary password** once, to share privately. The user must change it at the first
sign-in; until then the session can only change the password, read `/me` and sign out. Resetting a
password works the same way and also unlocks the account and signs the user out everywhere. The CLI
(`rowbird user create --password-stdin`) can set a chosen, non-temporary password for automation.

## 2. Create a connection

1. Form rendered from the connector's config schema (+ common SSH tunnel section).
2. **Test connection** (`POST /connections/test` with unsaved config, or `/connections/{id}/test`):
   returns ok/latency or a stable error code (`auth_failed`, `host_unreachable`, `tls_required`,
   `database_not_found`, `ssh_auth_failed`, `timeout`) with a human message.
3. **Write-permission check** (best effort per driver). If the DB user can write, show a warning
   recommending a read-only user. Never blocks.
4. On save, introspect the schema into `schema_cache` (tables, columns, types, comments). Refresh
   manually or automatically on test.

## 3. Write and test a query

1. Editor with syntax highlighting and schema autocomplete.
2. Optional **AI assistant** (off until an admin chooses a provider, ADR-0025): the user describes
   the query; the server sends the request, the connection's dialect, the current SQL and the cached
   schema (without `ai_excluded_tables`, never rows or samples; tables the request or the SQL name
   first, within about 60,000 characters) to the configured provider, and asks for structured JSON.
   The answer becomes a proposal `{sql, explanation, suggested_name, params, schedule?, warnings}`:
   named parameters that are not built-in are suggested as text; a schedule is kept only if it parses
   (`warnings` says `ai.warning.schedule_invalid` otherwise); SQL that may write or holds several
   statements is flagged (`not_read_only`, `multiple_statements`); a cut schema is flagged
   (`schema_truncated`). The editor shows it as a diff; Apply replaces the SQL and adds the
   parameters. Nothing is run or saved by the server. The report editor has the same assistant for
   schedules: a description in words becomes cron and time zone (default: the report's), shown with
   its description and next runs before Apply.
3. **Preview** (`POST /queries/preview`): always read-only, runs with a row cap (default 100, at
   most 1000, never above the connection's `max_rows`) and a short timeout (30s, never above the
   connection's), and returns columns (normalized types), rows, whether the result was truncated,
   duration, the resolved parameter values and the time zone used. The SQL does not need to be
   saved first.
4. Save → creates a new immutable `QueryVersion`. If the query is used by reports, the UI shows impact
   ("used by 3 reports") before saving.

### Parameters

- Syntax: `{{name}}`, where `name` matches `[A-Za-z_][A-Za-z0-9_]*` (spaces inside the braces are
  allowed).
- Built-ins resolved in the **report timezone**: `{{now}}` (datetime), and the dates `{{today}}`,
  `{{yesterday}}`, `{{start_of_week}}`, `{{start_of_month}}`, `{{start_of_last_month}}`,
  `{{end_of_last_month}}`, `{{start_of_year}}`. Weeks start on **Monday** (ISO 8601). The preview
  uses the browser's time zone, sent by the UI, and falls back to the workspace default time zone.
- User-defined params have a type (`text`, `integer`, `decimal`, `boolean`, `date`, `datetime`) and
  an optional default, overridable per report. They cannot reuse a built-in name. Saving requires
  every parameter used in the SQL to be a built-in or defined; unused definitions are allowed (the
  UI flags them and only saves the ones still in use).
- Parser MUST ignore `{{` inside SQL string literals, quoted identifiers, dollar-quoted bodies and
  comments, and MUST bind values as driver placeholders (`$n` pg, same number for a repeated
  parameter; `?` mysql/sqlite, one argument per occurrence; `@pN` mssql). Never interpolate.
- Values reach the driver as: integer `int64`, decimal as its exact string, boolean `bool`, text as
  a string, date as `YYYY-MM-DD`, datetime as the local wall time in the resolved time zone (with the
  UTC offset on Postgres, so `timestamptz` compares correctly and `timestamp` sees the local time).
  Dates and datetimes as text work on all four databases because the database converts them in the
  context of the column.

## 4. Create a report

1. Choose the query and, optionally, values for its user parameters (empty ones use the default;
   parameters without a default need one).
2. Schedule: visual builder (every day at, weekdays at, weekly on, monthly on day, every hour at,
   every N minutes, custom cron) + timezone (default: the workspace's) + **next 5 runs** preview with
   a description (`POST /schedules/preview`). Expressions have five fields or are one of `@hourly`,
   `@daily`, `@weekly`, `@monthly`, `@yearly`; seconds and `@every` are refused, so the minimum
   interval is 1 minute. An expression that never matches a date is invalid (ADR-0019).
3. Conditions (04-plugins).
4. Deliveries (on the report page, once the report is saved): pick one or many channels; for each:
   mode (only the destination's modes), formats, inline row limit, link expiry and login
   requirement, and the destination's options (recipients, templates, path); live **message
   preview** rendered by the destination (`POST /deliveries/preview`) with the first rows of the
   report's latest result, or none before its first run. The preview sends nothing, creates no link
   and stores no file.
5. Advanced: retries, misfire policy, overlap policy, auto-pause threshold, row limit, param overrides.
6. **Send test to me** (`POST /reports/{id}/test-delivery`): runs now with trigger `test` and
   delivers only to the current user's email through the system mailer (with every file format the
   report's deliveries use, as attachments, and links valid for 24 hours), or, with `channel_id`,
   through the report's deliveries on that channel. It delivers even when the condition does not
   hold, never counts toward failures or `changed`, and fails with `409 delivery.no_system_mailer`
   when no system mailer is set.

## 5. Run lifecycle

1. **Queue** (scheduler, every tick): lock enabled reports with `next_run_at <= now`; in one
   transaction, insert `Run{status: pending, trigger: schedule, scheduled_for}` and set `next_run_at`
   to the next occurrence **after now** (misfire handling below). A report never gets two runs for
   the same slot (unique index). Local workers are woken at once.
2. **Claim** (worker): take the oldest pending run whose `available_at` has passed and mark it
   `running` with this instance's id. **Overlap:** for scheduled runs only, if a run of the same
   report is `running`, apply `overlap_policy`: `skip` → the new run ends `skipped` with
   `error_code=run.overlap`; `queue` → it stays pending until the previous one finishes. Manual runs
   always start (ADR-0020).
3. **Execute** (worker): run the query's **current version** through the same executor as the
   preview: resolve parameters in the report's time zone with the report's overrides, open the
   connection (via SSH tunnel if configured), read-only transaction where supported, server-side
   statement timeout (the connection's), bind params, stream rows to a spool file in
   `$ROWBIRD_DATA_DIR/spool` (typed), stop at the row limit (the report's `max_rows` or the
   connection's, whichever is lower; set `truncated=true`). Record `query_version_id`,
   `resolved_params`, `row_count`, `duration_ms`, `result_hash` and a sample of the first 100 rows.
4. **Condition:** evaluate; if false → `skipped` with `run.condition_false`, stop. A rule that cannot
   be evaluated (unknown column, value of the wrong type) fails the run without retries.
5. **Format:** each file format a delivery needs is generated **once** from the spool, on first
   use, and stored as an Artifact in artifact storage (reused by all deliveries and resends).
   Inline renderings are produced per destination family (HTML, Markdown, text) with that
   destination's limits and stored as artifacts too, so a resend shows the same table (ADR-0023).
6. **Deliver:** only runs with `deliver` go out. A run that succeeded goes to every enabled delivery;
   a skipped or failed one only to destinations that report every run (Uptime Kuma, as down or as
   the configured status for skipped). Deliveries run in parallel (at most 4 at a time), each with
   its own retries. In `attachment` mode, files that do not fit the destination's limit (all files
   of a message together) or that it cannot take become shared links (**link fallback**, noted in
   the message and in the attempt's `meta.fallback_to_link`). In `link` mode every file is a link.
   Links need `ROWBIRD_BASE_URL`; without it a delivery that needs one fails with
   `delivery.base_url_missing`. Each delivery records one DeliveryAttempt and updates its channel's
   health.
7. **Finish:** set run status (`partial` with `run.delivery_failed` when a successful run had a
   failed delivery), update report `consecutive_failures`, `last_result_hash`, channel health,
   metrics, SSE events, notifications. Runs that succeeded, were partial or were skipped keep their
   spool for 24 hours (`result_expires_at`), so the result can be downloaded in any file format from
   the run page and a partial run's resends can still generate files no delivery had needed; other
   runs remove it. Files the deliveries generated stay downloadable from the run until the artifacts
   expire. Only scheduled runs and manual runs with delivery count: a failure extends the streak, a
   success, a partial run or a skip resets it, and only a success or a partial run (the query
   delivered a result) moves `last_result_hash`. Cancelled runs change nothing.

## 6. Failures and edge cases

- **Query/format failure:** retry up to `retry_max` times: the run goes back to `pending` with the
  next `attempt` and `available_at = now + retry_backoff_seconds * 2^(attempt-1)` (at most one
  hour), then `failed`. Failures another attempt cannot fix are not retried: authentication, missing
  database, TLS required, path not allowed, SSH authentication and host key, multiple statements,
  read-only violation, blocked network, invalid parameters, missing query and condition errors.
- **Delivery failure:** retry that delivery only (3 tries, 2 s then 4 s apart, each send bounded by
  5 minutes); only errors the destination marks as transient (unreachable, timeout, rate limited,
  server errors) are retried; never re-run the query. Run becomes `partial`. UI offers **resend**
  (single, `POST /runs/{id}/attempts/{attemptId}/retry`) and **resend all failed for channel X**
  (bulk, `POST /channels/{id}/retry-failed`: attempts that failed in the last 24 hours by default,
  at most 100, sent in the background) using stored artifacts. A resend reuses the attempt row, adds
  its tries to `attempts`, creates new links (the links of the failed message stay valid until they
  expire or are revoked) and, once every attempt of a partial run is sent, turns the run into a
  `success` (ADR-0023). Only failed attempts of runs that finished can be resent (`409
  delivery.not_failed`), and not test emails to the caller or attempts whose delivery was deleted
  (`409 delivery.not_resendable`).
- **Auto-pause:** after `auto_pause_after` consecutive `failed` runs that count (0 disables it), set
  `enabled=false`, `paused_reason=auto_failures`, `next_run_at=null`, notify owner + system alert
  (notifications arrive in roadmap phase 7). Resuming clears the streak and schedules from now.
- **Misfire (server was down):** a slot more than 2 minutes late is missed. `run_once` → one
  catch-up run for the missed slot immediately, then resume schedule; `skip` → just compute next
  occurrence. Never enqueue multiple missed runs.
- **DST:** occurrences computed in the report timezone; a nonexistent local time runs at the next
  valid instant (the end of the gap); an ambiguous time runs once (first occurrence) (ADR-0019).
- **Cancel:** user can cancel a pending/running run. A pending run is `cancelled` at once; a running
  run gets `cancel_requested_at` and its worker cancels the query server-side (immediately on the
  same instance, within a tick on another).
- **Instance lost:** a running run whose instance stopped sending heartbeats for three ticks (at
  least 30 seconds) fails with `run.instance_lost` and is retried per policy. With SQLite (a single
  instance) this happens as soon as the server starts again.
- **Shutdown:** the instance stops claiming, waits up to `ROWBIRD_SHUTDOWN_TIMEOUT` for running runs,
  then cancels them (`run.shutdown`).

## 7. Manual run

`POST /reports/{id}/run` with `deliver: false` (preview only; the result sample is shown on the run,
it never counts toward failures or `changed`) or `deliver: true` (full run, the default). Both have
trigger `manual`, run even when the report is paused and ignore the overlap policy. With
`Idempotency-Key`, a repeated request for the same report within 24 hours returns the first run
(`200` instead of `202`).

## 8. Notifications and alerting (layered)

1. **In-app (always works):** notification center (bell), stored in the internal DB; **health banner**
   when a channel is failing or a report failed its latest counted run or was paused after failures;
   per-channel health (last success/failure/error) on the Channels list.
2. **What is notified** (ADR-0024):
   - A channel's first failed delivery or saved-channel test after it worked (or was never used)
     opens `channel_failing`; every further failure is counted on it; the first success resolves it
     and adds `channel_recovered`.
   - A counted run (scheduled, or manual with delivery) that ends `failed` opens `report_failing`;
     further failures are counted; the next `success`, `partial` or `skipped` counted run resolves
     it and adds `report_recovered`. Partial runs are notified through their channel instead.
   - Auto-pause adds `report_paused`, resolved when someone resumes the report.
   Admins receive every notification; the report owner too when `notify_owner_on_failure`.
3. **System alert channels:** a primary and a fallback channel, each with the delivery options of
   its destination (recipients, chat). Opening a group and each recovery send one alert to the
   primary; when sending there fails (30 s per try), or the alert is about the primary itself, it
   goes to the fallback. Further occurrences of an open group send nothing. The UI warns when both
   channels have the same type. Alerts are plain title and text in the workspace language, with a
   link to the channel, report or run when `ROWBIRD_BASE_URL` is set; S3 channels cannot carry them.
   Sending an alert never changes a channel's health, so an alert cannot cause another. Alerts wait
   in a bounded in-memory queue (100) sent by a background worker. The report owner also gets an
   email through the system mailer, in their own language, when `notify_owner_on_failure`.
4. **External watchdog:** instances with a scheduler send `GET heartbeat_url` every
   `heartbeat_interval` while the store answers and the scheduler completed a tick within three
   ticks; the URL is secret and failures are logged by code only. `/health/*` and `/metrics` serve
   external monitoring (08).
5. **Real time:** changes reach the UI as server-sent events (05); views refetch on the events that
   concern them.
