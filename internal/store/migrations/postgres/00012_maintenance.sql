-- +goose Up
-- Daily internal jobs (retention). An instance takes a job by setting a lease; the others skip it
-- until the lease expires, so each job runs once per period across instances.
CREATE TABLE maintenance_jobs (
    name        TEXT PRIMARY KEY NOT NULL,
    last_run_at TIMESTAMPTZ,
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ
);
CREATE INDEX artifacts_created_idx ON artifacts (workspace_id, created_at) WHERE deleted_at IS NULL;
CREATE INDEX runs_created_idx ON runs (workspace_id, created_at);
CREATE INDEX security_events_created_idx ON security_events (workspace_id, created_at);

-- +goose Down
DROP INDEX security_events_created_idx;
DROP INDEX runs_created_idx;
DROP INDEX artifacts_created_idx;
DROP TABLE maintenance_jobs;
