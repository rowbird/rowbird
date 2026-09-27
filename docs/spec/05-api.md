# 05: API

## Approach (ADR-0005)

**Spec-first.** `api/openapi.yaml` (OpenAPI 3.1 where tooling allows, else 3.0.3; it is 3.0.3 today
because oapi-codegen does not fully support 3.1) is the source of truth. Generated: Go strict-server interfaces (`oapi-codegen`), TypeScript types/client
(`openapi-typescript` + `openapi-fetch`), and the endpoint list of the docs site
(`docs.rowbird.dev/reference/api-endpoints`, built from this file). The server does not serve API
docs itself (decided in Phase 12: it would add a viewer to the binary for what the docs site covers).

Base path: `/api/v1`. JSON only (except file downloads and SSE). Paths in the OpenAPI document are
absolute, so the public endpoints outside `/api/v1` (`/health/*`) are part of the same contract.
Two are exceptions and are served by their own handlers: `GET /r/{token}`, a page for browsers (a
file, a redirect to storage or to the login page, or a short HTML page in the visitor's language
for errors), documented here and in 07, and `GET /metrics`, the Prometheus text format, documented
in 08. `GET /api/v1/events` is in the document as a `text/event-stream` operation, so it goes
through the same authorization as every other operation.

## Authentication

- **Browser:** session cookie `rowbird_session` (`HttpOnly`, `Secure` when `ROWBIRD_BASE_URL` is
  https, `SameSite=Lax`) + CSRF token for state-changing requests: the server sets the readable
  cookie `rowbird_csrf` and the client echoes it in `X-CSRF-Token`. The token is an HMAC of the session
  id, so it is useless for any other session.
- **Sign-in:** `POST /auth/login` with email and password. When the account has two-factor
  authentication the answer is `status: mfa_required` with a challenge token and `methods` (`totp`,
  `passkey`), and `POST /auth/login/second-factor` finishes with a TOTP code, a recovery code or a
  passkey (`passkey`: the answer of `navigator.credentials.get` to the options of
  `POST /auth/login/second-factor/passkey`).
- **Passkeys:** `POST /auth/passkey/options` returns WebAuthn options (binary values in base64url)
  and a challenge token; `POST /auth/passkey` with the browser's answer signs in without a password.
  Each ceremony is stored server side, answered once and valid for 5 minutes. Passkeys need
  `ROWBIRD_BASE_URL` (its host is the relying party); without it these answer
  `409 passkey.unavailable` and `GET /setup/status` says `passkeys_available: false`.
- **Automation:** `Authorization: Bearer rbk_<random>` API keys with scopes `read`, `run`, `write`,
  `admin`. Stored as hash; shown once at creation. Only admins create and manage keys. A key acts
  with its scopes, limited by its owner's current role, and stops working when the owner is disabled.
  Sending both a session cookie and a bearer key is rejected.
- **Request format:** state-changing requests with a body must be `application/json` (`415`
  otherwise). A cross-site HTML form cannot send JSON without a CORS preflight, which protects the
  unauthenticated endpoints (setup, login) from CSRF.
- **Forgot password:** `POST /auth/password-reset {email}` always answers `202` (rate limited per
  IP) and emails a one-use link valid for 30 minutes when the account exists, is active and has a
  password, and the workspace has a system mailer and `ROWBIRD_BASE_URL` (at most once every 5
  minutes per account). `POST /auth/password-reset/confirm {token, password}` sets the password,
  unlocks the account and signs it out everywhere (`410 auth.reset_invalid` for an unknown, used or
  expired link); it does not sign in. `GET /setup/status` says `password_reset_available`.
- **OIDC:** `GET /auth/oidc/login?redirect=<app path>` redirects to the provider (authorization code
  with PKCE S256; state, nonce and verifier travel in the encrypted, `HttpOnly`, `SameSite=Lax`
  cookie `rowbird_oidc`, valid 10 minutes, path `/api/v1/auth/oidc`); the provider sends the browser
  to `GET /auth/oidc/callback`, which sets the session cookies and redirects to the app path, or to
  `/login?oidc_error=<code>` (`auth.oidc_failed`, `auth.oidc_no_account`,
  `auth.oidc_domain_not_allowed`, `auth.oidc_email_unverified`, `auth.oidc_unavailable`). The
  redirect URI is `ROWBIRD_BASE_URL` + `/api/v1/auth/oidc/callback`. `GET /setup/status` says
  whether to offer the button (`oidc_enabled`, `oidc_label`).
- **Rate limits:** login, its second factor and the passkey sign-in (per IP + per account,
  progressive lockout), AI proposals (per user, 20 per
  10 minutes on each instance, `429 ai.too_many_requests` with `Retry-After`), shared link downloads
  (per IP, 60 per minute).

## Permissions (OSS)

| Role | Can |
|---|---|
| viewer | read everything except secrets; download artifacts; view runs |
| editor | viewer + create/edit queries, reports, deliveries; run reports; preview and test deliveries, test saved channels, resend deliveries, revoke links; use AI |
| admin | editor + connections, channels, users, API keys, settings, import |

API key scopes are ordered: `read` < `run` < `write` < `admin`; each includes the ones before it.

Each operation declares its rule in `api/openapi.yaml` with `x-rowbird-authz` (ADR-0017): minimum
role, minimum scope, `session_only` (account self-service that API keys cannot call) and
`allow_restricted`. A session is **restricted** while the user must change a temporary password
(`auth.password_change_required`) or must enroll a second factor because the workspace requires it
(`auth.mfa_setup_required`); it can then only call operations marked `allow_restricted` (read `/me`,
change the password, enroll TOTP or add a passkey, sign out). Sessions started through OIDC are
never restricted: the provider handles the password and the second factor. Operations without a rule are denied, and the server
refuses to start.
## Resources (standard CRUD unless noted)

`/connections`, `/queries` (+ `/queries/{id}/versions`, read-only list/get, and `/restore`),
`/reports`, `/reports/{id}/deliveries` (list in order, create, `PUT` replaces one, delete),
`/channels` (each with its health, its capabilities for its configuration and `used_by`, the
deliveries that use it; deleting one in use answers `409 channel.in_use`), `/runs` (read-only;
`GET /runs/{id}` includes `files`, the artifacts its deliveries generated, and `deliveries`, one
attempt per delivery), `/links` (list newest first with `run_id` and `active` filters, get with the
latest 100 downloads (with `user_name` for signed-in downloaders), revoke; links are created only by deliveries and the token is never shown
again), `/users`, `/api-keys`, `/notifications` (the caller's own, newest first, with `unread`
filter and `unread_count`; `POST /notifications/{id}/read`, `POST /notifications/read-all`),
`/dashboard` (home page and health banner: next runs, recent failed and partial runs, success rate
of 7 and 30 days, failing channels and reports, and `onboarding`: whether the workspace has a
connection, a query, a report and a sent delivery), `/settings/alerts` (admin: primary and fallback
alert channels with their options, heartbeat URL (write-only, `heartbeat_url_configured`) and
interval, `same_destination` warning; `PUT` replaces, leaving `heartbeat_url` out keeps it and `""`
removes it), `/settings` (`links_enabled`, read-only, says
whether `ROWBIRD_BASE_URL` is set; `retention_runs_days` and `retention_artifacts_days`, 1 to
3650; `update_check`), `/me` (profile with `has_password`, sessions, passkeys, 2FA),
`/me/passkeys` (list; `POST /me/passkeys/options` with the current password, then `POST /me/passkeys`
with the browser's answer and a name; `PATCH` renames; `POST /me/passkeys/{id}/remove` with the
current password; accounts without a password skip the confirmation), `/settings/oidc` (admin: the
provider, client id, scopes, button label, auto-provisioning with a default role of viewer or editor,
allowed email domains; the client secret is write-only, `client_secret_configured`; `redirect_uri`
to register at the provider), `/system/storage` (admin: storage backend and size, when the
retention job last ran, scheduled backup status), `/system/about` (version, and the latest release
when the update check found one), `/security-events` (admin, read-only).

## Actions

| Endpoint | Purpose |
|---|---|
| `GET /setup/status`, `POST /setup` | first-run wizard |
| `POST /connections/test`, `POST /connections/{id}/test` | test unsaved / saved connection; answers `200` with `ok` and, on failure, `error_code` (plus `error_detail.fingerprint` for SSH host keys). With `connection_id`, secret placeholders resolve from that saved connection. |
| `POST /connections/{id}/schema/refresh` | re-introspect schema |
| `POST /queries/preview` | run unsaved SQL with parameters, row cap and timeout (see 03 §3) |
| `POST /queries/{id}/restore` | copy an old version into a new one (`number`, `version`) |
| `POST /reports/{id}/run` | manual run (`deliver`, default true); `202` with the run, `200` with the earlier run for a repeated `Idempotency-Key` |
| `POST /reports/{id}/test-delivery` | "send test to me": queues a run with trigger `test` and answers `202` with it; without `channel_id` emails the caller through the system mailer (`409 delivery.no_system_mailer` when there is none), with it uses the report's deliveries on that channel (`validation.no_delivery` when there are none) |
| `POST /reports/{id}/pause`, `/resume` | enable/disable; resume clears the failure streak |
| `POST /schedules/preview` | cron + tz → next N occurrences (at most 20), human description in `locale` |
| `POST /deliveries/preview` | render a delivery (saved or not) for its destination with the report's latest result: subject, body (`html` or `text`) and the names of attachments and links; sends nothing, creates no link, stores no file |
| `GET /runs/{id}/result?format=csv` | the run's result as a file: the stored artifact, exactly as delivered, when a delivery generated that format (until the artifact expires); otherwise generated from the spool in any file format while `result_expires_at` has not passed (`410 run.result_unavailable` after), in the caller's language and the report's time zone |
| `POST /runs/{id}/cancel` | cancel pending/running; `409 run.not_active` once finished |
| `POST /runs/{id}/attempts/{attemptId}/retry` | resend one failed delivery with the run's stored files and answer with the attempt (03 §6); `409 delivery.not_failed` or `delivery.not_resendable` |
| `POST /channels/{id}/retry-failed` | bulk resend the channel's attempts that failed since `since` (default 24 hours ago), at most 100, in the background; `202` with how many were queued |
| `POST /channels/test` | test unsaved channel settings (admin); with `channel_id`, secret placeholders resolve from that saved channel |
| `POST /channels/{id}/test` | send a test message through a saved channel (email goes to `to`, default the caller) and record the outcome as its health; answers `200` with `ok` and, on failure, `error_code` |
| `POST /links/{id}/revoke` | revoke shared link |
| `POST /export` | YAML export (`application/yaml`) of reports (each with its query) and queries by slug, or the whole workspace, optionally with the connections and channels they use; secrets become `${env:...}` placeholders (editor, `read`) |
| `POST /import` | YAML import: `yaml`, `dry_run` (default true), `policy` for conflicts, per-item `items` policies, `map` for missing references, `secrets` for `${env:NAME}` placeholders; answers the plan (items with their action and changes, missing references, needed secrets, errors with file and line) and whether it was applied; a dry run applies and rolls back (admin, `admin`) |
| `POST /gitops/detach`, `POST /gitops/attach` | move a resource (`kind`, `id`) from the configuration directory to the UI and back; `409 gitops.not_managed` / `gitops.not_detached` (admin) |
| `POST /ai/generate` | AI proposal for `task` `query` (needs `connection_id`; optional current `sql`) or `schedule` (a description in words; `timezone` is the default): `sql`, `explanation`, `suggested_name`, `params`, `schedule` (`cron`, `timezone`, `description`, `next`), `warnings`, `model`; nothing is run or saved; `409 ai.not_configured`, `409 ai.no_schema` (schema never read), `422 ai.*` for provider failures and unusable answers (editor, `run`) |
| `GET /ai/status` | whether the assistant is configured, and its provider id (viewer) |
| `GET /settings/ai`, `PUT /settings/ai` | the AI provider and its settings, secrets as `{configured}`; an empty provider turns the assistant off and removes the settings (admin) |
| `POST /settings/ai/test` | check AI settings before saving (key and model); failures answer `200` with `ok: false` and `error_code` (admin) |
| `POST /settings/oidc/test` | read the provider's discovery document for an issuer; `200` with `ok` and, on failure, `error_code` (admin) |
| `GET /plugins` | all plugins with metadata, schemas, capabilities, translations and, for destinations, `delivery_schema` |
| `GET /events` | **SSE** stream (run status, report changes, channel health, the caller's notifications); viewer, `read` scope |

Public (no session): `GET /health/live`, `GET /health/ready`, `GET /metrics` (optional bearer
`ROWBIRD_METRICS_TOKEN`), `GET /r/{token}` (shared link download: `200` with the file, `302` to a
presigned storage URL, `302` to `/login?redirect=/r/<token>` when the link requires a session in
its workspace, `404` unknown token, `410` expired, revoked or file removed, `429` over 60
downloads per minute per IP).

## Conventions

- **IDs:** UUIDv7 strings. **Times:** RFC 3339 UTC.
- **Pagination:** cursor-based: `?limit=50&cursor=...` → `{ "items": [...], "next_cursor": "..." }`.
- **Filtering/sorting:** query params (`status=failed,skipped&report_id=...&trigger=manual&from=...`).
  Runs are listed newest first; `GET /runs/{id}` adds the error message, the query version, the
  resolved parameters, the condition result and the result sample.
- **Errors:** RFC 9457 `application/problem+json`:
  ```json
  { "type": "https://rowbird.dev/errors/connection.auth_failed", "title": "Authentication failed",
    "status": 422, "code": "connection.auth_failed", "i18n_key": "errors.connection.auth_failed",
    "detail": "...", "errors": [ { "field": "port", "code": "validation.range" } ] }
  ```
- **Optimistic concurrency:** updates send `version`; mismatch → `409` with `code: conflict.version`.
- **Secrets in responses:** never returned; `{ "password": { "configured": true } }`. Sending the
  placeholder back (or leaving the field out) means "unchanged"; `null` removes the secret; a string
  replaces it. Field errors inside a plugin configuration are reported as `config.<key>`.
- **Failed operations on external systems** (for example refreshing the schema of a database that
  refuses the connection) answer `422` with the connector's error code.
- **Blocked deletes:** deleting something still in use answers `409` with an `in_use` code and a
  `dependents` array (`type`, `id`, `name`) so the UI can show the impact.
- **Result rows** (preview, later run results) are arrays in column order. Integers beyond 2^53,
  decimals, dates, times and JSON are strings (so JavaScript never loses precision); timestamps are
  RFC 3339; binary is base64; non-finite floats are strings.
- **Idempotency:** `Idempotency-Key` on `POST /reports/{id}/run` (24h window); test deliveries do not take one yet.
- **Versioning:** `/v1` gets only additive changes; breaking changes go to `/v2` with a deprecation
  period announced in the changelog.

## SSE events

`run.updated {run_id, report_id, status}` (queued, started, retried, finished, cancelled, resent),
`report.updated {report_id}` (paused, resumed), `channel.health {channel_id, ok}` (every recorded
outcome) and `notification.created {id, type, count}` (only to its recipient, also when an
occurrence is counted on an open notification). The stream starts with `retry: 5000`, sends a
`: ping` comment every 15 s and sets `X-Accel-Buffering: no`; reverse proxies must not buffer it.
Events come from the instance serving the stream (ADR-0024): with several instances a run executed
elsewhere is not announced, so the UI keeps polling slowly (every 15 s) while it shows something
unfinished, and quickly (2 s) while the stream is down. Clients refetch on events; the payloads
only say what changed.
