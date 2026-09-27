# Configuration

Rowbird is configured with environment variables, flags or a config file. When a setting comes from
more than one place, the first of these wins:

1. command-line flags (`--listen-addr :9090`)
2. environment variables (`ROWBIRD_LISTEN_ADDR=:9090`)
3. the `server` section of a config file
4. built-in defaults

## Config file

Pass the path with `--config` or `ROWBIRD_CONFIG_FILE`. Keys are the variable names in lower case
without the `ROWBIRD_` prefix; the S3 groups are nested:

```yaml
server:
  base_url: https://rowbird.example.com
  listen_addr: ":8080"
  workers: 8
  log_format: json
  trusted_proxies: ["10.0.0.0/8"]
  backup_schedule: "0 3 * * *"
  storage_backend: s3
  storage_s3:
    endpoint: https://s3.eu-central-1.amazonaws.com
    region: eu-central-1
    bucket: acme-rowbird
```

Keep secrets (keys, passwords, the database URL) in the environment or in files, not in the config
file.

## Variables

### Core

| Variable | Default | Description |
|---|---|---|
| `ROWBIRD_BASE_URL` | none | The public URL of Rowbird, such as `https://rowbird.example.com`. Links in messages (reports, runs, shared files), passkeys and secure cookies need it. Without it messages carry no links, the `link` mode is refused and attachments that would fall back to a link fail with `delivery.base_url_missing`. Rowbird warns at startup when it is unset. |
| `ROWBIRD_LISTEN_ADDR` | `:8080` | The HTTP listen address. |
| `ROWBIRD_DATA_DIR` | `/data` | The SQLite store, local artifacts (`artifacts/`), the generated master key, run spools (`spool/`, kept 24 hours) and scheduled backups. |
| `ROWBIRD_DATABASE_URL` | `sqlite://$ROWBIRD_DATA_DIR/rowbird.db` | The internal store: `sqlite://path` or `postgres://user:password@host/db`. See [Scaling](./scaling). |
| `ROWBIRD_CONFIG_FILE` | none | Path of a config file whose `server` section is loaded (also `--config`). |
| `ROWBIRD_SETUP_TOKEN` | none | When set, the setup wizard asks for this value, so only you can create the first admin. |
| `ROWBIRD_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `ROWBIRD_LOG_FORMAT` | `text` | `text` or `json`. |
| `ROWBIRD_UPDATE_CHECK` | `true` | Check GitHub once a day for a newer release. Admins can also turn it off in Settings > About. |

### Master key {#master-key}

The master key encrypts every stored secret (AES-256-GCM): connection and channel passwords, TOTP
secrets, AI keys, secret settings. It is 32 random bytes, base64 encoded
(`openssl rand -base64 32`). When you provide none, Rowbird generates one into
`$ROWBIRD_DATA_DIR/master.key` and logs a warning to back it up.

| Variable | Default | Description |
|---|---|---|
| `ROWBIRD_MASTER_KEY` | generated | The key itself. |
| `ROWBIRD_MASTER_KEY_FILE` | none | A file holding the key, for Docker and Kubernetes secrets. |
| `ROWBIRD_MASTER_KEY_PREVIOUS` | none | An older key accepted for decryption only, while a [rotation](./key-rotation) reaches every instance. |
| `ROWBIRD_MASTER_KEY_PREVIOUS_FILE` | none | The same, from a file. |

### Scheduler and workers

| Variable | Default | Description |
|---|---|---|
| `ROWBIRD_WORKERS` | `4` | Runs executed at the same time by this instance. |
| `ROWBIRD_SCHEDULER_TICK` | `5s` | How often the scheduler looks for due reports. It also paces heartbeats and the recovery of runs left by a lost instance. |
| `ROWBIRD_SHUTDOWN_TIMEOUT` | `30s` | How long running runs may take to finish when Rowbird stops. |
| `ROWBIRD_NO_SCHEDULER` | `false` | Do not schedule reports on this instance (also `--no-scheduler`). |
| `ROWBIRD_NO_WORKERS` | `false` | Do not execute runs on this instance (also `--no-workers`). |

### Artifact storage

Files generated for deliveries (and served by download links) are kept in one place for the whole
instance.

| Variable | Default | Description |
|---|---|---|
| `ROWBIRD_STORAGE_BACKEND` | `local` | `local` (under `$ROWBIRD_DATA_DIR/artifacts`) or `s3`. |
| `ROWBIRD_STORAGE_S3_ENDPOINT` | none | The S3-compatible endpoint, such as `https://s3.amazonaws.com` or `http://minio:9000`. The scheme decides TLS. Required for `s3`. |
| `ROWBIRD_STORAGE_S3_REGION` | none | The bucket's region. |
| `ROWBIRD_STORAGE_S3_BUCKET` | none | The bucket. Required for `s3`. |
| `ROWBIRD_STORAGE_S3_ACCESS_KEY` | none | Access key id. |
| `ROWBIRD_STORAGE_S3_SECRET_KEY` | none | Secret access key. |
| `ROWBIRD_STORAGE_S3_PATH_STYLE` | `false` | Path-style URLs, for MinIO and similar servers. |
| `ROWBIRD_STORAGE_S3_PREFIX` | none | Keep objects under this folder of a shared bucket. |

Artifacts record their backend, but only the configured one is read: changing the backend makes
earlier files unreachable, and their links answer 410 Gone.

### Backups

Scheduled backups cover a SQLite store. See [Backups and restore](./backups).

| Variable | Default | Description |
|---|---|---|
| `ROWBIRD_BACKUP_SCHEDULE` | none | A cron expression in UTC, such as `0 3 * * *`. Empty turns scheduled backups off. |
| `ROWBIRD_BACKUP_DIR` | `$ROWBIRD_DATA_DIR/backups` | Where backups are written. |
| `ROWBIRD_BACKUP_KEEP` | `7` | How many backups to keep, in the directory and in the bucket. |
| `ROWBIRD_BACKUP_WITH_ARTIFACTS` | `false` | Include local artifacts in each backup. |
| `ROWBIRD_BACKUP_S3_ENDPOINT` | none | Also upload each backup to this S3-compatible endpoint. |
| `ROWBIRD_BACKUP_S3_REGION` | none | Region of the backup bucket. |
| `ROWBIRD_BACKUP_S3_BUCKET` | none | The backup bucket. Uploads happen only when it is set. |
| `ROWBIRD_BACKUP_S3_ACCESS_KEY` | none | Access key id. |
| `ROWBIRD_BACKUP_S3_SECRET_KEY` | none | Secret access key. |
| `ROWBIRD_BACKUP_S3_PATH_STYLE` | `false` | Path-style URLs. |
| `ROWBIRD_BACKUP_S3_PREFIX` | none | Folder for the backups inside the bucket. |

### Network and security

| Variable | Default | Description |
|---|---|---|
| `ROWBIRD_TRUSTED_PROXIES` | none | Comma-separated CIDRs allowed to set `X-Forwarded-For` and `X-Forwarded-Proto`. Set it to your reverse proxy's address, or client IPs (used for rate limits and the security log) will be the proxy's. |
| `ROWBIRD_NETWORK_POLICY` | `open` | `open`, or `block-private` to stop connections, channels and AI providers from reaching private, loopback, link-local and cloud metadata addresses. See [Hardening](/security/hardening#network-policy). |
| `ROWBIRD_SQLITE_DIRS` | `$ROWBIRD_DATA_DIR/sqlite` | Comma-separated absolute directories that SQLite connections may read. |
| `ROWBIRD_METRICS_TOKEN` | none | When set, `/metrics` requires `Authorization: Bearer <token>`. |

### GitOps

| Variable | Default | Description |
|---|---|---|
| `ROWBIRD_CONFIG_DIR` | none | A directory of YAML documents applied at startup, or right after setup. See [Config as code](/reference/config-as-code#gitops-directory). |

### CLI

| Variable | Default | Description |
|---|---|---|
| `ROWBIRD_API_KEY` | none | The API key `rowbird apply` and `rowbird export` use with `--server`. |

## Settings in the UI

Some settings belong to the workspace rather than the instance and are changed by admins in the
UI: the system mailer, alert channels and heartbeat, retention of runs and files, requiring 2FA,
OIDC sign-in, the AI assistant and the update check.
