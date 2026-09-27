# CLI

The `rowbird` binary is the server and its administration tool. Commands that change the store
(`migrate`, `backup`, `restore`, `keys`, `user`) read the same configuration as the server, so run
them with the same environment. In Docker: `docker exec rowbird /rowbird <command>`, or
`docker compose run --rm rowbird <command>` when the server must be stopped.

## Global flags

Every flag has an environment variable (see [Configuration](/operations/configuration)); flags win.

| Flag | Variable |
|---|---|
| `--config` | `ROWBIRD_CONFIG_FILE` |
| `--base-url` | `ROWBIRD_BASE_URL` |
| `--data-dir` | `ROWBIRD_DATA_DIR` |
| `--database-url` | `ROWBIRD_DATABASE_URL` |
| `--listen-addr` | `ROWBIRD_LISTEN_ADDR` |
| `--log-format`, `--log-level` | `ROWBIRD_LOG_FORMAT`, `ROWBIRD_LOG_LEVEL` |
| `--workers` | `ROWBIRD_WORKERS` |
| `--scheduler-tick` | `ROWBIRD_SCHEDULER_TICK` |
| `--shutdown-timeout` | `ROWBIRD_SHUTDOWN_TIMEOUT` |
| `--no-scheduler`, `--no-workers` | `ROWBIRD_NO_SCHEDULER`, `ROWBIRD_NO_WORKERS` |

## `rowbird serve`

Runs the server: API, web UI, scheduler and workers. It is the default command. It migrates the
store at startup and stops gracefully on `SIGTERM` or `SIGINT`, giving running reports
`--shutdown-timeout` to finish.

## `rowbird migrate`

Applies pending migrations to the internal store and exits.

## `rowbird backup`

```bash
rowbird backup -o file [--with-artifacts]
```

Writes a backup of a SQLite store while the server runs to `-o` (`--output`); `-o -` writes to standard output.
`--with-artifacts` includes local artifact storage. Refuses on PostgreSQL. See
[Backups](/operations/backups).

## `rowbird restore`

```bash
rowbird restore file [--force]
```

Replaces the SQLite store with a backup. Stop the server first; a server seen alive in the last two
minutes makes it refuse unless `--force`. Keeps the current database as `.pre-restore`.

## `rowbird keys rotate`

```bash
rowbird keys rotate [--new-key-file file] [--dry-run] [--force]
```

Encrypts every stored secret with a new master key in one transaction. `--dry-run` rolls back.
`--force` rotates even while instances using another key are running. See
[Master key rotation](/operations/key-rotation).

## `rowbird apply`

```bash
rowbird apply -f <file|dir> [--dry-run] [--policy fail|overwrite|copy|skip] [--map old=new] [--server url] [--api-key key]
```

Creates or updates resources from the YAML documents in `-f` (`--file`, a file or a directory), printing the plan. Exits 1 when the plan is
blocked or the apply fails. With `--server` it works through the API with an API key
(`--api-key` or `ROWBIRD_API_KEY`) and sends the `${env:...}` values from its own environment.
See [Config as code](./config-as-code).

## `rowbird export`

```bash
rowbird export [-o file] [--report slug]... [--query slug]... [--with-connections] [--with-channels] [--server url] [--api-key key]
```

Writes reports (each with its query) and queries as YAML to `-o` (`--output`, default standard output); everything when no slug is given. Secrets
become `${env:...}` placeholders.

## `rowbird user`

Administration directly on the local store, for recovering access. Actions are recorded in the
security log as CLI actions.

| Command | Does |
|---|---|
| `user create --email e --name n [--role viewer\|editor\|admin] [--locale en\|pt-BR] [--password-stdin]` | creates a user and prints a temporary password, or reads one from standard input |
| `user reset-password --email e` | sets a new temporary password, unlocks the account and signs it out everywhere |
| `user disable-2fa --email e` | turns off TOTP and removes passkeys |
| `user set-role --email e --role r` | changes the role; the last active admin cannot be demoted |

## `rowbird healthcheck`

Exits 0 when `/health/ready` on the local listen address answers 200, 1 otherwise. `--url` probes
another address. Used by the Docker image's `HEALTHCHECK`.

## `rowbird version`

Prints the version, commit, build date, Go version and platform.
