# Docker

The image is `ghcr.io/rowbird/rowbird`, for `linux/amd64` and `linux/arm64`.

```bash
docker run -d --name rowbird \
  --restart unless-stopped \
  -p 8080:8080 \
  -v rowbird-data:/data \
  -e ROWBIRD_BASE_URL=https://rowbird.example.com \
  ghcr.io/rowbird/rowbird:1.0.0
```

## Tags

| Tag | Meaning |
|---|---|
| `1.2.3` | exactly that release; use this in production |
| `1.2` | the latest patch of 1.2 |
| `1` | the latest 1.x release |
| `latest` | the latest release |

Prereleases (`1.3.0-rc.1`) only get their exact tag.

## The image

- Based on `gcr.io/distroless/static-debian12:nonroot`: CA certificates and the Rowbird binary,
  no shell and no package manager. Time zones are built into the binary.
- Runs as user and group `65532`. `/data` belongs to that user and is declared as a volume.
- Listens on port `8080`.
- `HEALTHCHECK` runs `rowbird healthcheck`, which calls `/health/ready`.
- The entry point is `rowbird` and the default command is `serve`, so any subcommand runs with
  `docker run ... ghcr.io/rowbird/rowbird:1.0.0 <command>` or, in a running container,
  `docker exec rowbird /rowbird <command>`.

## Data

Everything Rowbird keeps lives in `/data` unless you configure otherwise:

| Path | What |
|---|---|
| `/data/rowbird.db` | the SQLite internal store |
| `/data/master.key` | the generated master key (when you do not provide one) |
| `/data/artifacts/` | files generated for deliveries (local storage) |
| `/data/spool/` | full run results, kept 24 hours for downloads |
| `/data/backups/` | scheduled backups |
| `/data/sqlite/` | where SQLite connections may read databases from |

When you mount a host directory instead of a named volume, it must be writable by uid 65532:

```bash
sudo install -d -o 65532 -g 65532 /srv/rowbird
docker run ... -v /srv/rowbird:/data ghcr.io/rowbird/rowbird:1.0.0
```

## Administration commands

```bash
docker exec rowbird /rowbird version
docker exec rowbird /rowbird backup -o /data/backups/manual.tar.gz
docker exec -it rowbird /rowbird user reset-password --email admin@example.com
```

See the [CLI reference](/reference/cli) for every command.

For HTTPS, put Rowbird behind a [reverse proxy](/operations/reverse-proxy), or use the
[Compose setup](./compose) that includes Caddy.
