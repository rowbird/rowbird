# Observability

## Health

| Endpoint | Answers |
|---|---|
| `GET /health/live` | `200` while the process is up. Use it for liveness probes. |
| `GET /health/ready` | `200` when every component is healthy, `503` otherwise. Use it for readiness probes and load balancers. |

The readiness body lists the components and never contains secrets:

```json
{"status":"ok","components":{"store":{"status":"ok"},"migrations":{"status":"ok"},"scheduler":{"status":"ok"}}}
```

- `store`: the internal database answers.
- `migrations`: the schema is up to date.
- `scheduler` (only on instances that schedule): a tick completed within the last three ticks.

`rowbird healthcheck` calls `/health/ready` on the local listen address and exits 0 or 1; the
Docker image uses it as its `HEALTHCHECK`.

## Metrics

`GET /metrics` serves Prometheus metrics. It is public unless `ROWBIRD_METRICS_TOKEN` is set; then
it requires `Authorization: Bearer <token>`.

```yaml
scrape_configs:
  - job_name: rowbird
    scheme: https
    authorization:
      credentials_file: /etc/prometheus/rowbird-token
    static_configs:
      - targets: [rowbird.example.com]
```

| Metric | Type | Meaning |
|---|---|---|
| `rowbird_runs_total{status,trigger}` | counter | runs that finished, by status (`success`, `partial`, `skipped`, `failed`, `cancelled`) and trigger |
| `rowbird_run_duration_seconds` | histogram | duration of finished runs, deliveries included |
| `rowbird_query_rows` | histogram | rows returned by report queries |
| `rowbird_deliveries_total{destination,status}` | counter | delivery sends (`sent` or `failed`), one per send with its retries |
| `rowbird_delivery_duration_seconds{destination}` | histogram | duration of delivery sends |
| `rowbird_queue_pending` | gauge | runs waiting for a worker |
| `rowbird_storage_bytes` | gauge | bytes of stored artifacts |
| `rowbird_scheduler_lag_seconds` | gauge | how late the latest due run was queued |
| `rowbird_build_info{version}` | gauge | always 1; the version is the label |

The Go runtime and process collectors are included. Counters and histograms are per instance;
`rowbird_queue_pending` and `rowbird_storage_bytes` are read from the store, so every instance
reports the same value.

Useful alerts:

```promql
# Runs failing
increase(rowbird_runs_total{status="failed"}[1h]) > 0
# Deliveries failing
increase(rowbird_deliveries_total{status="failed"}[1h]) > 0
# Work piling up
rowbird_queue_pending > 20
# The scheduler is late
rowbird_scheduler_lag_seconds > 60
```

## Logs

Rowbird logs with Go's `slog`: `ROWBIRD_LOG_FORMAT=json` for collectors, `text` for people.
Every line about a run carries `run_id` and `report_id`, so one run can be followed from queue to
delivery. HTTP requests are logged with a `request_id`, also returned as `X-Request-ID`. Successful
health probes and metrics scrapes are logged at `debug`.

Secrets are removed from logs by a redacting handler: connection passwords, tokens, URLs with
credentials and keys never appear, not even at `debug`.

## Inside Rowbird

- The **home page** shows upcoming runs, recent failures, success rates and failing channels.
- The **notification center** lists problems with reports, channels and backups.
- **Alert channels** (Settings > Alerts) send the same problems to email, Slack or any channel,
  with a fallback channel and a message when the problem is resolved.
- A **heartbeat URL** (Settings > Alerts) is called on an interval while Rowbird is healthy.
  Point it at an Uptime Kuma push monitor or Healthchecks.io to learn when Rowbird itself is down.
