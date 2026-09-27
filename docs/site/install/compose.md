# Docker Compose

[`deploy/compose`](https://github.com/rowbird/rowbird/tree/main/deploy/compose) is a production
setup for one server: Rowbird with SQLite in a volume, a master key kept as a Docker secret,
nightly backups, and [Caddy](https://caddyserver.com) in front with automatic HTTPS.

```bash
cp .env.example .env        # set ROWBIRD_VERSION and ROWBIRD_DOMAIN
mkdir -p secrets
openssl rand -base64 32 > secrets/master.key
sudo chown 65532:65532 secrets/master.key && sudo chmod 400 secrets/master.key
docker compose up -d
```

The image runs as uid 65532 and Compose mounts the secret file with its host owner and mode, so
the file must belong to that user.

::: warning Keep a copy of the master key
Store `secrets/master.key` outside the server (a password manager, a vault). Backups cannot be
read without it, and a lost key means re-entering every connection and channel password.
:::

## What it configures

| Variable | Value | Why |
|---|---|---|
| `ROWBIRD_BASE_URL` | `https://$ROWBIRD_DOMAIN` | links in messages, passkeys, secure cookies |
| `ROWBIRD_MASTER_KEY_FILE` | `/run/secrets/master_key` | the key stays out of the data volume and its backups |
| `ROWBIRD_TRUSTED_PROXIES` | `172.16.0.0/12` | the Compose network, so client addresses from Caddy are trusted |
| `ROWBIRD_LOG_FORMAT` | `json` | for log collectors |
| `ROWBIRD_BACKUP_SCHEDULE` | `0 3 * * *` | a backup every night at 03:00 UTC, keeping 14 |
| `ROWBIRD_METRICS_TOKEN` | from `.env` | protects `/metrics`, which Caddy exposes |

Every variable is described in [Configuration](/operations/configuration).

## Behind another proxy

Remove the `caddy` service, publish the port (`ports: ["127.0.0.1:8080:8080"]`), and follow
[Reverse proxies](/operations/reverse-proxy). Make sure `ROWBIRD_TRUSTED_PROXIES` covers the
proxy's address.

## Upgrading

Change `ROWBIRD_VERSION` in `.env`, then:

```bash
docker compose pull
docker compose up -d
```

Rowbird backs up the SQLite store before it migrates. See [Upgrades](/operations/upgrades).
