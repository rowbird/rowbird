-- +goose Up
CREATE TABLE reports (
    id                      TEXT PRIMARY KEY NOT NULL,
    workspace_id            TEXT NOT NULL REFERENCES workspaces (id),
    title                   TEXT NOT NULL,
    slug                    TEXT NOT NULL,
    description             TEXT NOT NULL DEFAULT '',
    query_id                TEXT NOT NULL REFERENCES queries (id),
    enabled                 INTEGER NOT NULL DEFAULT 1,
    cron                    TEXT NOT NULL,
    timezone                TEXT NOT NULL,
    next_run_at             TIMESTAMP,
    condition               TEXT NOT NULL DEFAULT '{"match":"all","rules":[]}',
    param_overrides         TEXT NOT NULL DEFAULT '{}',
    max_rows                INTEGER,
    retry_max               INTEGER NOT NULL DEFAULT 2,
    retry_backoff_seconds   INTEGER NOT NULL DEFAULT 30,
    misfire_policy          TEXT NOT NULL DEFAULT 'run_once',
    overlap_policy          TEXT NOT NULL DEFAULT 'skip',
    auto_pause_after        INTEGER NOT NULL DEFAULT 5,
    consecutive_failures    INTEGER NOT NULL DEFAULT 0,
    paused_reason           TEXT,
    owner_id                TEXT,
    notify_owner_on_failure INTEGER NOT NULL DEFAULT 1,
    last_result_hash        TEXT,
    created_at              TIMESTAMP NOT NULL,
    updated_at              TIMESTAMP NOT NULL,
    created_by              TEXT,
    version                 INTEGER NOT NULL DEFAULT 1,
    UNIQUE (workspace_id, slug)
);
CREATE INDEX reports_due_idx ON reports (enabled, next_run_at);
CREATE INDEX reports_query_idx ON reports (query_id);

CREATE TABLE runs (
    id                  TEXT PRIMARY KEY NOT NULL,
    workspace_id        TEXT NOT NULL REFERENCES workspaces (id),
    report_id           TEXT NOT NULL REFERENCES reports (id) ON DELETE CASCADE,
    trigger             TEXT NOT NULL,
    triggered_by        TEXT,
    deliver             INTEGER NOT NULL DEFAULT 1,
    status              TEXT NOT NULL,
    scheduled_for       TIMESTAMP,
    available_at        TIMESTAMP NOT NULL,
    started_at          TIMESTAMP,
    finished_at         TIMESTAMP,
    duration_ms         BIGINT,
    query_version_id    TEXT,
    resolved_params     TEXT,
    row_count           BIGINT,
    truncated           INTEGER NOT NULL DEFAULT 0,
    condition_result    TEXT,
    result_hash         TEXT,
    result_sample       TEXT,
    attempt             INTEGER NOT NULL DEFAULT 1,
    error_code          TEXT,
    error_message       TEXT,
    instance_id         TEXT,
    idempotency_key     TEXT,
    cancel_requested_at TIMESTAMP,
    created_at          TIMESTAMP NOT NULL,
    updated_at          TIMESTAMP NOT NULL,
    created_by          TEXT,
    version             INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX runs_queue_idx ON runs (status, available_at);
CREATE INDEX runs_report_idx ON runs (report_id, id);
CREATE UNIQUE INDEX runs_slot_idx ON runs (report_id, scheduled_for);
CREATE INDEX runs_idempotency_idx ON runs (report_id, idempotency_key);

CREATE TABLE instances (
    id           TEXT PRIMARY KEY NOT NULL,
    hostname     TEXT NOT NULL,
    version      TEXT NOT NULL,
    started_at   TIMESTAMP NOT NULL,
    heartbeat_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE instances;
DROP TABLE runs;
DROP TABLE reports;
