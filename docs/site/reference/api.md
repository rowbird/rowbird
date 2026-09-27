# HTTP API

Everything the web UI does goes through the same HTTP API, so everything can be automated. The
contract is [`api/openapi.yaml`](https://github.com/rowbird/rowbird/blob/main/api/openapi.yaml)
(OpenAPI 3.0); the [endpoint list](./api-endpoints) is built from it.

## Basics

- Base path `/api/v1`. JSON in and out, except file downloads and the event stream.
- IDs are UUIDv7 strings. Times are RFC 3339 in UTC.
- Requests that change something and carry a body must be `Content-Type: application/json`.

## Authentication

For automation, create an API key (Settings > API keys, admins only) and send it as a bearer token:

```bash
curl -H "Authorization: Bearer $ROWBIRD_API_KEY" https://rowbird.example.com/api/v1/reports
```

A key has a scope (`read` < `run` < `write` < `admin`) and never exceeds its owner's role. Each
endpoint states the role and scope it needs. Some account operations (changing a password,
passkeys) accept only browser sessions.

The browser uses a session cookie plus a CSRF token: the server sets the `rowbird_csrf` cookie and
the client sends it back in `X-CSRF-Token` on requests that change something. Sending a cookie and a
bearer key together is rejected.

## Examples

Run a report now and deliver it:

```bash
curl -X POST -H "Authorization: Bearer $ROWBIRD_API_KEY" -H "Content-Type: application/json" \
  -H "Idempotency-Key: nightly-2026-09-27" \
  -d '{"deliver": true}' \
  https://rowbird.example.com/api/v1/reports/$REPORT_ID/run
```

The answer is `202` with the queued run. Repeating the request with the same `Idempotency-Key`
within 24 hours returns the same run.

List the latest failed runs:

```bash
curl -H "Authorization: Bearer $ROWBIRD_API_KEY" \
  "https://rowbird.example.com/api/v1/runs?status=failed&limit=20"
```

Download a run's result as Excel:

```bash
curl -H "Authorization: Bearer $ROWBIRD_API_KEY" -o result.xlsx \
  "https://rowbird.example.com/api/v1/runs/$RUN_ID/result?format=xlsx"
```

## Conventions

**Pagination** is by cursor: `?limit=50&cursor=...` returns `{"items": [...], "next_cursor": "..."}`.
Pass `next_cursor` back until it is absent.

**Errors** are RFC 9457 problem documents with a stable `code`:

```json
{
  "type": "https://rowbird.dev/errors/connection.auth_failed",
  "title": "Authentication failed",
  "status": 422,
  "code": "connection.auth_failed",
  "i18n_key": "errors.connection.auth_failed",
  "errors": [{ "field": "port", "code": "validation.range" }]
}
```

Match on `code`, never on `title`, which is translated.

**Optimistic concurrency.** Updates send the resource's current `version`; if someone changed it
meanwhile, the answer is `409` with `conflict.version`.

**Secrets** are never returned: a secret field reads `{"configured": true}`. Sending that back, or
leaving the field out, keeps the stored value; `null` removes it; a string replaces it.

**Deleting something in use** answers `409` with an `in_use` code and the `dependents`.

**Result rows** are arrays in column order. Decimals, integers beyond 2^53, dates, times and JSON
are strings, so no precision is lost.

**Versioning.** `/v1` only gets additive changes. Breaking changes would go to `/v2`, announced in
the changelog.

## Events

`GET /api/v1/events` is a [server-sent events](https://developer.mozilla.org/docs/Web/API/Server-sent_events)
stream: `run.updated`, `report.updated`, `channel.health` and `notification.created`. Payloads only
say what changed; fetch the resource for details. The stream covers the instance serving it.

## Public endpoints

No authentication: `GET /health/live`, `GET /health/ready`, `GET /metrics` (unless
`ROWBIRD_METRICS_TOKEN` is set) and `GET /r/{token}`, the shared link downloads.
