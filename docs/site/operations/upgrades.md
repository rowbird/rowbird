# Upgrades

Rowbird follows [semantic versioning](https://semver.org). Patch and minor releases upgrade in
place; a major release may need steps, which its release notes list first under "Breaking
changes". Read the notes of every version you skip.

## How an upgrade works

1. Stop the old version and start the new one.
2. At startup the new version takes a lock in the database, so only one instance migrates.
3. With SQLite, if there are migrations to apply, it first copies the database to
   `rowbird.db.pre-migrate-v<old schema version>.bak` next to it.
4. It applies the migrations and starts serving.

`rowbird migrate` applies the migrations and exits, if you prefer to migrate as a separate step.

## Docker Compose

```bash
# .env: ROWBIRD_VERSION=1.1.0
docker compose pull
docker compose up -d
docker compose logs -f rowbird
```

## Binary

Replace the binary and restart the service:

```bash
sudo install -m 0755 rowbird /usr/local/bin/rowbird
sudo systemctl restart rowbird
```

## Several instances

Upgrade them one after another. The first to start migrates while holding the lock; the others
wait for it. Old and new versions should not run side by side for long: stop the old ones promptly.

## Going back

There are no automatic downgrades: an older binary refuses a schema newer than it knows. To go
back, stop Rowbird, restore the database from before the upgrade, and start the older version:

- SQLite: `rowbird restore` a backup, or copy `rowbird.db.pre-migrate-v<N>.bak` over `rowbird.db`
  (remove `rowbird.db-wal` and `rowbird.db-shm` first).
- PostgreSQL: restore your `pg_dump` from before the upgrade.

Runs and changes made after the upgrade are lost, which is why a quick decision matters.

## Knowing a new version exists

With the update check on (the default), Settings > About shows the latest release once a day. It
calls `api.github.com` with nothing but a `User-Agent`. Turn it off with `ROWBIRD_UPDATE_CHECK=false`
or in Settings > About. You can also watch releases on GitHub.
