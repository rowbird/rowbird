# Scaling

One instance with SQLite handles a lot: queries run on your databases, not in Rowbird, and
`ROWBIRD_WORKERS` (default 4) sets how many reports run at once. Raise it before adding instances.

## SQLite: one instance

With the default SQLite store, run exactly one instance. Two processes on the same database file
would both schedule reports.

## PostgreSQL: several instances

Point every instance at the same PostgreSQL database:

```bash
ROWBIRD_DATABASE_URL=postgres://rowbird:secret@db.internal:5432/rowbird?sslmode=verify-full
ROWBIRD_STORAGE_BACKEND=s3
ROWBIRD_STORAGE_S3_ENDPOINT=https://s3.eu-central-1.amazonaws.com
ROWBIRD_STORAGE_S3_REGION=eu-central-1
ROWBIRD_STORAGE_S3_BUCKET=acme-rowbird
```

- **Same master key** on every instance.
- **Shared artifact storage.** Use `s3` (or one volume mounted by all) so any instance can serve
  downloads, resends and links for runs another instance executed.
- **No leader.** Every instance schedules and executes; due runs are claimed with
  `FOR UPDATE SKIP LOCKED`, so each runs once. Maintenance jobs (retention) take a lease in the
  database.
- **Lost instances.** Each instance keeps a heartbeat. Runs left `running` by an instance whose
  heartbeat stopped are marked `failed` with `instance_lost` and retried according to the report.
- **Roles.** `ROWBIRD_NO_SCHEDULER` and `ROWBIRD_NO_WORKERS` split the work, for example two
  instances that only execute and one that also schedules.

### Things to know

- Live updates in the browser come from the instance serving the page. Runs executed elsewhere
  appear through the UI's slower polling (every 15 seconds).
- The full result of a run (for downloads in formats no delivery produced) stays on the instance
  that ran it for 24 hours. Stored artifacts are served by any instance.
- System alerts are queued in memory on the instance that saw the problem; a crash can lose one
  queued alert, while the in-app notification is already stored.
- Backups: use `pg_dump`; `rowbird backup` covers SQLite only.
