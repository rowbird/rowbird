# 08: Operations

## Installation

- **Docker (primary):** `ghcr.io/rowbird/rowbird:<version>` multi-arch (amd64, arm64), distroless,
  non-root (uid 65532), volume `/data`.
- **Docker Compose:** the one-liner in the README; `deploy/compose` for production (Caddy, master
  key as a Docker secret, scheduled backups); `deploy/demo` to try it.
- **Binaries** for Linux, macOS, Windows (amd64/arm64) on GitHub Releases; systemd unit example in
  `deploy/systemd`.
- **Kubernetes:** plain example manifests in `deploy/k8s` (no Helm chart in v1).

## Configuration

Precedence: flags > environment > config file (`rowbird.yaml` server section, path given by
`--config` or `ROWBIRD_CONFIG_FILE`) > defaults. Keys in the file are the variable names in lower case
without the prefix (`listen_addr`, `workers`, ...).

| Variable | Default | Purpose |
|---|---|---|
| `ROWBIRD_MASTER_KEY` / `_FILE` | generated into data dir | secret encryption key |
| `ROWBIRD_MASTER_KEY_PREVIOUS` / `_FILE` | none | an older key accepted for decryption only, while a rotation is rolled out |
| `ROWBIRD_DATABASE_URL` | `sqlite://$ROWBIRD_DATA_DIR/rowbird.db` | internal store (`postgres://...` supported) |
| `ROWBIRD_DATA_DIR` | `/data` | SQLite, local artifacts (`artifacts/`), generated key, run spools (`spool/`, kept 24 hours; with several instances only the one that ran a report can generate a format no delivery produced, while stored artifacts can be served by any instance that shares the storage) |
| `ROWBIRD_BASE_URL` | none (warn if unset) | public URL for links in messages (report and run pages, shared links `/r/...`); without it messages carry no links, the `link` mode is refused and attachments that would fall back to links fail with `delivery.base_url_missing` |
| `ROWBIRD_LISTEN_ADDR` | `:8080` | HTTP listen address |
| `ROWBIRD_WORKERS` | `4` | concurrent runs per instance |
| `ROWBIRD_SCHEDULER_TICK` | `5s` | scheduler polling interval; also paces heartbeats and recovery |
| `ROWBIRD_SHUTDOWN_TIMEOUT` | `30s` | how long running runs may take to finish on shutdown |
| `ROWBIRD_NO_SCHEDULER` / `ROWBIRD_NO_WORKERS` | `false` | disable a role on this instance (also `--no-scheduler`, `--no-workers`) |
| `ROWBIRD_STORAGE_BACKEND` | `local` | artifact storage for the whole instance: `local` (under `$ROWBIRD_DATA_DIR/artifacts`) or `s3` |
| `ROWBIRD_STORAGE_S3_ENDPOINT`, `_REGION`, `_BUCKET`, `_ACCESS_KEY`, `_SECRET_KEY`, `_PATH_STYLE`, `_PREFIX` | none | the S3-compatible bucket for `s3` storage (endpoint and bucket required; the endpoint's scheme decides TLS; the prefix keys objects under a folder of a shared bucket). Artifacts record their backend, but only the configured one is read: changing it makes earlier files unreachable (their links answer 410) |
| `ROWBIRD_LOG_LEVEL` / `_FORMAT` | `info` / `text` | `debug..error` / `text|json` |
| `ROWBIRD_METRICS_TOKEN` | none | bearer token for `/metrics` |
| `ROWBIRD_CONFIG_DIR` | none | GitOps directory applied at startup, or right after setup (09) |
| `ROWBIRD_NETWORK_POLICY` | `open` | `open` or `block-private` |
| `ROWBIRD_UPDATE_CHECK` | `true` | check GitHub releases for new versions once a day (admins can also turn it off in Settings > About) |
| `ROWBIRD_TRUSTED_PROXIES` | none | CIDRs allowed to set `X-Forwarded-For` and `X-Forwarded-Proto` |
| `ROWBIRD_BACKUP_SCHEDULE` | none | cron expression (UTC) for scheduled backups of a SQLite store; empty turns them off |
| `ROWBIRD_BACKUP_DIR` | `$ROWBIRD_DATA_DIR/backups` | where scheduled backups are written |
| `ROWBIRD_BACKUP_KEEP` | `7` | how many scheduled backups to keep (in the directory and in the bucket) |
| `ROWBIRD_BACKUP_WITH_ARTIFACTS` | `false` | include local artifacts in scheduled backups |
| `ROWBIRD_BACKUP_S3_ENDPOINT`, `_REGION`, `_BUCKET`, `_ACCESS_KEY`, `_SECRET_KEY`, `_PATH_STYLE`, `_PREFIX` | none | also upload each scheduled backup to this bucket |
| `ROWBIRD_SETUP_TOKEN` | none | when set, the setup wizard requires this value |
| `ROWBIRD_SQLITE_DIRS` | `$ROWBIRD_DATA_DIR/sqlite` | absolute directories (comma separated) SQLite connections may read |

Reverse proxy docs for Nginx, Caddy, Traefik (including SSE: disable buffering) are on the docs
site; its configuration page is checked against `Config` by a test.

## CLI

`rowbird serve` (default) · `migrate` · `backup [--with-artifacts] -o file|-` · `restore file
[--force]` · `keys rotate [--new-key-file file] [--dry-run] [--force]` · `apply -f <file|dir> [--dry-run] [--policy fail|overwrite|copy|skip] [--map old=new]`
(prints the plan like Terraform and exits with 1 when it is blocked or fails) ·
`export [-o file] [--report slug] [--query slug] [--with-connections] [--with-channels]` ·
`user create|reset-password|disable-2fa|set-role` (local store, for admin recovery: `create` and
`reset-password` print a temporary password; `create --password-stdin` sets a chosen one; the last
active admin cannot be demoted) · `healthcheck` (for Docker HEALTHCHECK) ·
`version`. The CLI talks to the local store directly (admin recovery) or to a remote instance via
`--server` + API key (`--api-key` or `ROWBIRD_API_KEY`; apply/export), in which case `apply` sends
the `${env:...}` values from its own environment.

## Backup & restore

- **SQLite:** `rowbird backup -o file` writes a `.tar.gz` without stopping the server: a
  `manifest.json` (format, Rowbird version, schema version, the master key's id, never the key,
  and the time), the database copied with `VACUUM INTO`, and with `--with-artifacts` the files of
  local artifact storage. `rowbird restore file` needs the server stopped (a heartbeat in the last
  two minutes refuses it without `--force`), refuses a backup from a newer schema, keeps the current
  database as `<db>.pre-restore`, and warns when the backup was taken with another master key.
- **Postgres:** `backup` and `restore` refuse and point to `pg_dump` / `pg_restore`.
- **Scheduled backups** (ADR-0028) are configured with `ROWBIRD_BACKUP_*` on the instance, SQLite
  only: `rowbird-backup-<UTC time>.tar.gz` in the backup directory (written to a temporary file,
  then renamed), optionally uploaded to a bucket, keeping the newest `ROWBIRD_BACKUP_KEEP` in both.
  A failure is logged and notifies the admins (`backup_failed`), resolved by the next success.
  Settings > Storage & retention shows the last and next backup.
- Docs emphasize: **back up the master key separately**; backups are useless without it.

## Master key rotation

`rowbird keys rotate` decrypts every encrypted value (connection and channel secrets, TOTP secrets,
secret settings) with the keys in use and encrypts it again with the new key, keeping each value's
associated data, in one transaction; `--dry-run` rolls back. A secret setting it does not know
stops it before anything changes. It records `keys_rotated` in every workspace.

- With the key the server generated in the data directory, the command creates the new key (or
  takes `--new-key-file`), writes it to `master.key.next` before the database changes, then keeps
  the old key as `master.key.previous` and moves the new one into place.
- With a configured key (`ROWBIRD_MASTER_KEY` or `_FILE`) it needs `--new-key-file`; the operator
  then configures the new key.
- Single instance: stop, rotate, start. Several instances on Postgres: restart them with the new key
  as `ROWBIRD_MASTER_KEY` and the current one as `ROWBIRD_MASTER_KEY_PREVIOUS`, rotate, then remove
  the previous key. Instances record the id of their key on their heartbeat, and the command
  refuses while one that runs workers still uses another key (`--force` overrides).
- Request forgery tokens derive from the key, so signed-in users sign in again after a rotation.

## Upgrades

Automatic migrations at startup guarded by a DB lock; automatic SQLite backup before migrating;
no automatic downgrades (restore backup instead): a binary refuses to start, and every command that
opens the store refuses, when the database was migrated by a newer version (`ErrSchemaNewer`). SemVer; changelog with breaking changes highlighted.

## Observability

- Logs: structured `slog`, `run_id`/`report_id` attributes on all run-related lines.
- Health: `/health/live` (process up), `/health/ready` (components `store`, `migrations` and, on
  instances with a scheduler, `scheduler`: a tick completed within the last three ticks). JSON body
  with component states (no secrets); 503 when one is unhealthy.
- Metrics (Prometheus text format at `/metrics`, public unless `ROWBIRD_METRICS_TOKEN` is set, then
  `Authorization: Bearer <token>`, compared in constant time): `rowbird_runs_total{status,trigger}`,
  `rowbird_run_duration_seconds`, `rowbird_query_rows`, `rowbird_deliveries_total{destination,status}`
  (`sent` or `failed`, one per send with its retries), `rowbird_delivery_duration_seconds{destination}`,
  `rowbird_queue_pending` and `rowbird_storage_bytes` (read from the store on each scrape, so every
  instance reports the same value), `rowbird_scheduler_lag_seconds` (how late the latest due run was
  queued), `rowbird_build_info{version}`, plus the Go runtime and process collectors. Counters and
  histograms are per instance. Each instance has its own registry.
- Heartbeat: outbound GET to the workspace's configured URL (e.g. Uptime Kuma push,
  Healthchecks.io), from instances with a scheduler, only while healthy (03 §8).

## Scaling & HA

- SQLite: single instance (documented).
- The multi-instance behavior is tested with two instances on one Postgres store (ADR-0028).
- Postgres: multiple instances, with `s3` artifact storage (or one volume shared by all) so that any
  instance can serve files, resends and links; claiming via `FOR UPDATE SKIP LOCKED`; no leader
  election; each instance has an `instance_id` recorded on runs; stale `running` runs (instance
  heartbeat lost) are recovered as `failed` with `error_code=instance_lost` and retried per policy.
- Real-time events stay inside the instance that produced them (ADR-0024); the UI's slow polling
  covers runs executed by other instances. System alerts are queued in memory on the instance that
  saw the failure, so a crash can lose a queued alert (the in-app notification is already stored).

## Privacy

No telemetry. The only default outbound call is the optional update check: once a day (the first a
minute after start), `GET https://api.github.com/repos/rowbird/rowbird/releases/latest` with only a
`User-Agent: rowbird/<version>` header. Drafts and prereleases are ignored; the answer is kept in
memory. It is off when `ROWBIRD_UPDATE_CHECK=false` or when an admin of any workspace turns it off.
