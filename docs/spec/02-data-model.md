# 02: Data model

## Common fields

Unless stated otherwise every entity has: `id` (UUIDv7, ADR-0008), `workspace_id`, `created_at`,
`updated_at` (UTC), `created_by` (user id, nullable for system), and `version` (int, optimistic
concurrency, incremented on every update). Soft-delete is NOT used except where noted; deletes are
blocked when dependents exist (API returns the dependents).

Encrypted columns (`*_enc`) hold AES-256-GCM ciphertext with key id prefix (07-security), bound to
their row with associated data (`connection:<id>:secrets`, `channel:<id>:secrets`), so a
ciphertext cannot be copied to another row or another kind of entity (ADR-0022).

## Tenancy & identity

**Workspace**: `name`, `slug`. OSS: exactly one, created at setup, never shown in UI.

**User** (global, not workspace-scoped): `email` (unique, lowercase), `name`,
`password_hash` (argon2id, nullable for OIDC-only users), `locale`, `theme` (`system|light|dark`),
`totp_secret_enc`, `totp_enabled`, `totp_last_step` (last accepted TOTP time step, for replay
protection), `recovery_codes` (SHA-256 hashes), `must_change_password` (set for temporary passwords),
`failed_logins`, `locked_until` (progressive lockout), `disabled_at`, `last_login_at`,
`oidc_subject`, `oidc_issuer`.

**WorkspaceMember**: `workspace_id`, `user_id`, `role` (`admin|editor|viewer`). OSS: every user is a
member of the default workspace.

`oidc_issuer` and `oidc_subject` identify the user at the OIDC provider (unique together).

**Passkey** (global, like users): `user_id`, `name`, `credential_id` (base64url, unique),
`credential` (the WebAuthn credential record as JSON: public key, flags, AAGUID, transports),
`sign_count`, `last_used_at`. Deleted with the user, and by an admin's 2FA reset.

**WebAuthnChallenge** (global): a passkey ceremony in progress. `token_hash`, `purpose`
(`register`, `login`, `mfa`), `user_id` (nullable for a passwordless sign-in), `data` (the session
data), `expires_at` (5 minutes). Removed when answered or by the retention job.

**Session**: `token_hash` (SHA-256), `user_id`, `workspace_id`, `expires_at`, `last_seen_at`, `ip`,
`user_agent`, `revoked_at`, `auth_method` (`password`, `passkey` or `oidc`; OIDC sessions are not
asked for Rowbird's second factor).

**LoginChallenge** (global): the pending second step of a sign-in. `token_hash`, `user_id`,
`expires_at` (5 minutes), `attempts` (at most 5). Deleted when used, exhausted or expired.

**PasswordReset** (global): a "forgot password" link. `user_id`, `token_hash` (SHA-256 of the `rbr_`
token, unique), `expires_at` (30 minutes), `used_at`. A new request replaces the user's earlier
ones; the retention job removes used and expired ones.

**ApiKey**: `name`, `user_id`, `prefix` (first 8 characters after `rbk_`, shown in UI), `key_hash`
(SHA-256), `scopes[]`
(`read`, `run`, `write`, `admin`), `expires_at`, `last_used_at`, `revoked_at`.

## Core

**Connection**: `name` (unique per workspace, slug-like `[a-z0-9][a-z0-9_-]*`, used as reference in
YAML; the driver cannot change after creation), `server_version` (from the last successful check),
`driver`
(plugin id), `config` (JSON, non-secret), `secrets_enc` (JSON blob), `query_timeout_seconds`
(default 60), `max_rows` (default 100000), `allow_multi_statement` (default false),
`ai_excluded_tables[]`, `schema_cache` (JSON), `schema_cached_at`, `has_write_permission`
(nullable bool, from last check), `status` (`unknown|ok|error`), `last_checked_at`, `last_error`.

**Query**: `title` (free text), `slug` (unique per workspace, derived from the title and editable;
this is the name config-as-code references), `description`, `connection_id`, `current_version_id`.
Changing the title, slug, description or connection updates the query itself (optimistic `version`)
and does not create a query version.

**QueryVersion** (immutable): `query_id`, `number` (1..n, unique per query), `sql` (up to 1 MB),
`params` (JSON array of user parameter definitions: `name`, `type`, `default`), `note`,
`restored_from` (nullable: the version number this one was copied from by a restore), `created_by`,
`created_at`. Saving creates a new version only when the SQL or the parameter definitions changed.
Restoring version N creates a new version with N's SQL and parameters; history is never rewritten.
Deleting a query deletes its versions. A connection used by queries cannot be deleted.

**Report**: `title` (free text), `slug` (unique per workspace, like queries), `description`,
`query_id` (runs the query's current version; a query used by reports cannot be deleted), `enabled`,
`cron` (five fields or a descriptor, see 03 §4), `timezone` (IANA, default the workspace's),
`next_run_at` (UTC, null when disabled), `condition` (JSON, see 04),
`param_overrides` (JSON: values by name for the query's user parameters), `max_rows` (nullable
override, never above the connection's), `retry_max` (default 2),
`retry_backoff_seconds` (default 30, exponential), `misfire_policy` (`run_once|skip`, default
`run_once`), `overlap_policy` (`skip|queue`, default `skip`), `auto_pause_after` (default 5),
`consecutive_failures`, `paused_reason` (nullable: `manual|auto_failures|gitops_orphan`), `owner_id`,
`notify_owner_on_failure` (default true), `last_result_hash` (for `changed` condition).

**Channel**: `name` (unique per workspace, slug-like `[a-z0-9][a-z0-9_-]*`, at most 63
characters), `type` (destination plugin id; cannot change after creation), `config` (JSON,
non-secret), `secrets_enc`, `status` (`unknown|ok|failing`), `last_success_at`, `last_failure_at`,
`last_error` (the destination's message with the channel's secrets removed), `is_system_mailer`
(bool, only for `email` channels, at most one per workspace; marking a channel unmarks the others).
The system mailer sends "send test to me" emails; invitations and password resets by email are not
implemented yet. Health is updated by every delivery attempt and every test of a saved channel. A
channel used by deliveries cannot be deleted.

**Managed by** (connections, channels, queries, reports): `managed_by` is `''` (the UI and the API),
`gitops` (applied from the configuration directory, read only through the API) or
`gitops_detached` (detached by an admin; the directory leaves it alone). Reports also carry
`gitops_orphan`: a GitOps report whose document left the directory, paused until it returns
(docs/spec/09-config-as-code.md).

**Delivery**: `report_id`, `channel_id`, `position`, `enabled`, `mode` (`inline|attachment|link`;
must be one of the destination's modes, default its first), `formats[]` (file formatter ids, e.g.
`["xlsx","csv"]`; required unless the mode is `inline`), `inline_row_limit` (1 to 200, default 20),
`include_inline_with_files` (bool, default true: files and links also carry the inline table when
the destination has one), `link_expires_seconds` (1 hour to 90 days, default 7 days),
`link_require_login` (default false), `options` (JSON validated by the destination's delivery
schema: recipients, subject/message templates, S3 path template, ...). At most 20 per report;
deleted with the report. The `link` mode needs `ROWBIRD_BASE_URL`.

## Execution

**Run**: `report_id`, `trigger` (`schedule|manual|test`; `retry` is reserved: resends reuse the
attempt instead of creating a run), `triggered_by`, `deliver` (false for manual runs that only show
the result), `test_target` (JSON, test runs only: `channel_id`, or the `email` of the user who asked), `status` (see below), `scheduled_for` (unique per report),
`available_at` (when the run may be claimed; retries push it forward), `started_at`, `finished_at`,
`duration_ms`, `query_version_id`, `resolved_params` (JSON: time zone and the bound values),
`row_count`, `truncated` (bool), `condition_result` (JSON: passed + per-rule details),
`result_hash` (SHA-256 of the columns and rows), `result_sample` (JSON: columns and at most the
first 100 rows, encoded like the preview; removed with the run by retention), `attempt`,
`error_code`, `error_message` (the database's message with the connection's secrets removed),
`instance_id`, `idempotency_key`, `cancel_requested_at`, `result_expires_at` (while the result can
be downloaded). A report's runs are deleted with it; a
report with a pending or running run cannot be deleted.

**Instance** (global): `id`, `hostname`, `version`, `started_at`, `heartbeat_at`. Each process
records a heartbeat every tick (ADR-0020).

Run status: `pending → running → success | partial | skipped | failed | cancelled`.
- `success`: all enabled deliveries sent (or zero deliveries, e.g. preview/manual without delivery).
- `partial`: query OK, at least one delivery failed after retries (`error_code=run.delivery_failed`).
  Resending until every attempt is sent turns it into `success`.
- `skipped`: condition false (`run.condition_false`), or the overlap policy skipped it
  (`run.overlap`).
- `failed`: query/formatting failed after retries (`error_code` is the connector's code, a condition
  code, `run.invalid_params`, `run.query_missing`, `run.instance_lost` or `run.internal`).
- `cancelled`: user cancel (`run.cancelled`) or shutdown (`run.shutdown`).

**DeliveryAttempt**: one row per delivery a run sent or tried to send. `run_id`, `delivery_id`
(nullable: test emails to the caller have no delivery row, and deleting the delivery keeps the
attempt), `channel_id` (nullable for the same reason), `status` (`pending|sending|sent|failed|skipped`),
`attempts` (tries so far, resends included), `last_error_code`, `last_error` (secrets removed),
`sent_at`, `meta` (JSON: mode, attachment and link file names, `fallback_to_link`, message ids, S3
key, HTTP status). A resend reuses the row (ADR-0023).

**Artifact**: a file generated once per run and format, shared by all its deliveries and resends.
`run_id`, `format` (unique per run: a file formatter id, or `inline:<formatter>:<rows>:<chars>:<link>`
for a stored inline rendering), `content_type`, `file_name` (`<report slug>-<YYYY-MM-DD-HHMM>.<ext>`
in the report's time zone), `storage_backend` (`local|s3`), `storage_key`
(`<workspace>/<report>/<run>/<file name>`), `size_bytes`, `sha256`, `expires_at` (now +
`retention_artifacts_days`), `deleted_at` (soft delete when retention removes the blob). Deleted with
the run.

**SharedLink**: `artifact_id`, `run_id`, `delivery_id` (nullable), `token_hash` (SHA-256 of the
`rbl_` token, unique), `expires_at`, `require_login`, `revoked_at`, `revoked_by`, `download_count`,
`last_download_at`. Every message that shares a file creates a new link, resends included.

**LinkDownload**: `link_id`, `at` (`created_at`), `user_id` (nullable: set when the downloader was
signed in to the workspace), `ip`, `user_agent` (at most 500 bytes).

## Observability & admin

**Notification**: one row per recipient (ADR-0024): `user_id` (the active admins, plus the
report's owner when `notify_owner_on_failure`; each reads and marks their own), `type`
(`channel_failing`, `channel_recovered`, `report_failing`, `report_recovered`, `report_paused`),
`severity` (`info|warning|error`), `title_key` (`notifications.<type>`, translated by the UI with
`params`), `params` (JSON: channel or report name, error, error code, failures), `entity_type`
(`channel|report`), `entity_id`, `group_key` (`channel:<id>:failing`, `report:<id>:failing`,
`report:<id>:paused`; empty for one-off notices), `count` (occurrences while open), `first_at`,
`last_at`, `read_at`, `resolved_at` (set when the problem ends; notices such as recoveries are
resolved from the start). At most one open notification per user and group (unique index): a new
occurrence increments `count`, moves `last_at`, takes the new params and makes it unread again.

**SecurityEvent**: `actor_user_id`, `type` (`login_success`, `login_failed`, `2fa_enabled`,
`api_key_created`, `connection_changed`, `user_role_changed`, ...), `ip`, `meta`, `created_at`.

**Setting**: `workspace_id`, `key`, `value` (JSON), `secret` (bool → stored encrypted). Keys:
`default_locale`, `default_timezone`, `system_alert_primary_channel_id` and
`system_alert_primary_options` (the delivery options for that channel: recipients, chat),
`system_alert_fallback_channel_id` and `system_alert_fallback_options`, `heartbeat_url` (secret,
encrypted with `setting:<workspace>:heartbeat_url` as associated data), `heartbeat_interval`
(seconds, 30 to 86400, default 60), `ai_provider` (an AI provider id; absent when the assistant is
off), `ai_config` (the provider's plain settings: model, base URL, timeout) and `ai_secrets` (secret:
the provider's secret fields, such as the API key, encrypted with `setting:ai:<workspace>:secrets` as
associated data, ADR-0022), `retention_runs_days` (90),
`retention_artifacts_days` (30), `require_2fa`, `update_check` (true; false in any workspace turns
the update check off), `oidc` (JSON: `enabled`, `issuer`, `client_id`, `scopes`, `button_label`,
`auto_provision`, `default_role`, `allowed_domains`) and `oidc_secret` (secret: the client secret,
encrypted with `setting:oidc:<workspace>:secrets` as associated data).

**MaintenanceJob** (global): `name`, `last_run_at`, `lease_owner`, `lease_until`. An instance takes
a job by setting the lease; a lease left by a crashed instance expires after an hour.

**Instance**: also records `key_id`, the master key it encrypts with, which `rowbird keys rotate`
compares with the new key.

**AuditLog**: reserved for EE.

## Retention

A daily internal job (`internal/maintenance`) runs on one instance at a time, through the
`retention` lease, and processes each workspace in batches:

- Runs in a final state created more than `retention_runs_days` ago are deleted with their
  attempts, artifact rows, links and link downloads; their files are removed first. Pending and
  running runs are never deleted.
- Artifact files created more than `retention_artifacts_days` ago are removed from storage (a file
  already gone counts as removed); the rows stay with `deleted_at`, so links to them answer 410
  Gone. Only files of the configured backend can be removed; others are only marked.
- Security events older than 365 days; notifications resolved more than 90 days ago (open ones stay,
  read or not, because new occurrences are counted on them).
- Sessions that expired or were revoked more than 30 days ago, and expired login and WebAuthn
  challenges.

A storage error stops the pass; the next day retries. Run spools are removed by the runner 24
hours after they were written.
