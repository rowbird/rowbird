# ADR-0028: Backups configured by the operator, key rotation with a previous key, retention by lease

- **Status:** Accepted
- **Date:** 2026-09-27

## Context

Phase 10 adds the operational jobs of docs/spec/08-operations.md: backup and restore, scheduled
backups, master key rotation and the daily retention job, and it tests several instances on one
Postgres store. The instance may be a single binary with SQLite or several instances sharing
Postgres; nothing may assume a leader, and secrets must stay readable during a key change.

## Decision

- **Backups are the operator's, configured by environment.** Scheduled backups use
  `ROWBIRD_BACKUP_*` like artifact storage does, not a workspace setting: they protect the whole
  instance, and a setting stored in the database being backed up would be lost with it. The UI only
  shows their status. They cover SQLite; Postgres has `pg_dump`, which the CLI points to.
- **A backup is a tar.gz with a manifest.** The database is copied online with `VACUUM INTO`; the
  manifest records the schema version and the master key's id, never the key. Restore refuses newer
  schemas and running servers, keeps the replaced database, and warns about another key.
- **Rotation re-encrypts in one transaction, with a previous key for rollouts.** Every encrypted
  value is listed with the associated data of its kind; an unknown secret setting stops the
  rotation, so a new kind of secret cannot be left behind silently. `ROWBIRD_MASTER_KEY_PREVIOUS`
  lets instances read both keys while they are restarted one by one. Instances record their key id
  on their heartbeat, so the CLI can tell whether the running ones already use the new key.
- **The generated key is replaced safely.** The new key is written next to the old one before the
  database changes, and moved into place after the commit; the old key is kept as
  `master.key.previous`.
- **Retention runs on one instance through a lease.** `maintenance_jobs` holds a lease per job: an
  instance takes it only when the job did not run in the last day and nobody holds it, and a lease
  left by a crash expires after an hour. Each instance checks hourly; there is no leader.
- **Run files go before rows.** Retention removes files first and marks artifact rows deleted, so
  links answer 410 Gone; a storage error stops the pass and leaves the rows for the next day.

## Consequences

- An operator on Postgres keeps using their database tooling for backups.
- A rotation signs users out of their CSRF tokens (they derive from the key) and is best done in a
  maintenance window.
- The multi-instance test (`app.TestInstancesShareAPostgresStore`) runs two full instances on one
  Postgres store: due reports run once, a lost instance's run is retried on the other, and
  retention runs once.
