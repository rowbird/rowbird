# Backups and restore

::: danger Back up the master key separately
Every secret in a backup (database passwords, channel tokens, TOTP secrets) is encrypted with the
master key, and the backup never contains the key. Keep the key somewhere else: a password
manager, a vault, a sealed envelope. A backup without its key restores reports and queries, but
every stored secret has to be entered again.
:::

What a backup contains depends on the internal store.

## SQLite

`rowbird backup` writes a `.tar.gz` while the server keeps running:

- `manifest.json`: the backup format, the Rowbird and schema versions, the id of the master key
  (never the key) and the time.
- the database, copied consistently with SQLite's `VACUUM INTO`.
- with `--with-artifacts`, the files in local artifact storage.

```bash
rowbird backup -o rowbird-backup.tar.gz
rowbird backup --with-artifacts -o - | ssh backup-host 'cat > rowbird.tar.gz'

# with Docker
docker exec rowbird /rowbird backup -o /data/backups/manual.tar.gz
```

### Scheduled backups

Set `ROWBIRD_BACKUP_SCHEDULE` (a cron expression in UTC) and Rowbird writes
`rowbird-backup-<UTC time>.tar.gz` to `ROWBIRD_BACKUP_DIR` (default `$ROWBIRD_DATA_DIR/backups`),
keeping the newest `ROWBIRD_BACKUP_KEEP` (default 7). Each file is written under a temporary name
and renamed when complete, so a crash never leaves a half backup with the final name.

```bash
ROWBIRD_BACKUP_SCHEDULE="0 3 * * *"
ROWBIRD_BACKUP_KEEP=14
```

A backup in the same volume as the database does not survive the loss of that volume. Upload each
one to a bucket as well with `ROWBIRD_BACKUP_S3_*`; the bucket keeps the same number of backups.
A failed backup is logged, notifies the admins (`backup_failed`) and is resolved by the next
success. Settings > Storage & retention shows the last and the next backup.

### Restore

A restore replaces the database, so the server must be stopped.

```bash
rowbird restore rowbird-backup.tar.gz
```

- It refuses when a server was alive in the last two minutes (its heartbeat is in the database).
  Pass `--force` only when you are sure it is stopped.
- It refuses a backup from a newer schema than this binary knows; upgrade first.
- The current database is kept next to it as `rowbird.db.pre-restore`.
- Artifacts in the backup are added to local storage.
- It warns when the backup was taken with another master key. Start the server with the key the
  backup was taken with.

With Docker Compose:

```bash
docker compose stop rowbird
docker compose run --rm rowbird restore /data/backups/rowbird-backup-20260927T030000Z.tar.gz
docker compose start rowbird
```

`docker compose run` uses the service's environment and volumes, so the same master key and data
directory apply.

### Test your backups

Restore one now and then on another machine or a scratch volume, start Rowbird with the same key,
and open a connection to check that its password still works. A backup you never restored is a
hope, not a backup.

## PostgreSQL

With `ROWBIRD_DATABASE_URL=postgres://...`, `rowbird backup` and `rowbird restore` refuse and point
to the database's own tools, which you probably already use:

```bash
pg_dump --format=custom --file=rowbird.dump "$ROWBIRD_DATABASE_URL"
pg_restore --clean --if-exists --dbname="$ROWBIRD_DATABASE_URL" rowbird.dump
```

Artifacts on S3 storage are not part of either; use the bucket's versioning or replication.

## Before upgrades

When a new version has migrations for a SQLite store, Rowbird first copies the database to
`rowbird.db.pre-migrate-v<old schema version>.bak`. See [Upgrades](./upgrades).
