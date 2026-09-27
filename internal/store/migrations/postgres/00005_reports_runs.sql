-- +goose Up
CREATE TABLE reports (
    id                      UUID PRIMARY KEY,
    workspace_id            UUID NOT NULL REFERENCES workspaces (id),
    title                   TEXT NOT NULL,
    slug                    TEXT NOT NULL,
    description             TEXT NOT NULL DEFAULT '',
    query_id                UUID NOT NULL REFERENCES queries (id),
    enabled                 BOOLEAN NOT NULL DEFAULT TRUE,
    cron                    TEXT NOT NULL,
    timezone                TEXT NOT NULL,
    next_run_at             TIMESTAMPTZ,
    condition               JSONB NOT NULL DEFAULT '{"match":"all","rules":[]}',
    param_overrides         JSONB NOT NULL DEFAULT '{}',
    max_rows                INTEGER,
    retry_max               INTEGER NOT NULL DEFAULT 2,
    retry_backoff_seconds   INTEGER NOT NULL DEFAULT 30,
    misfire_policy          TEXT NOT NULL DEFAULT 'run_once',
    overlap_policy          TEXT NOT NULL DEFAULT 'skip',
    auto_pause_after        INTEGER NOT NULL DEFAULT 5,
    consecutive_failures    INTEGER NOT NULL DEFAULT 0,
    paused_reason           TEXT,
    owner_id                UUID,
    notify_owner_on_failure BOOLEAN NOT NULL DEFAULT TRUE,
    last_result_hash        TEXT,
    created_at              TIMESTAMPTZ NOT NULL,
    updated_at              TIMESTAMPTZ NOT NULL,
    created_by              UUID,
    version                 BIGINT NOT NULL DEFAULT 1,
    UNIQUE (workspace_id, slug)
);
CREATE INDEX reports_due_idx ON reports (enabled, next_run_at);
CREATE INDEX reports_query_idx ON reports (query_id);

CREATE TABLE runs (
    id                  UUID PRIMARY KEY,
    workspace_id        UUID NOT NULL REFERENCES workspaces (id),
    report_id           UUID NOT NULL REFERENCES reports (id) ON DELETE CASCADE,
    trigger             TEXT NOT NULL,
    triggered_by        UUID,
    deliver             BOOLEAN NOT NULL DEFAULT TRUE,
    status              TEXT NOT NULL,
    scheduled_for       TIMESTAMPTZ,
    available_at        TIMESTAMPTZ NOT NULL,
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ,
    duration_ms         BIGINT,
    query_version_id    UUID,
    resolved_params     JSONB,
    row_count           BIGINT,
    truncated           BOOLEAN NOT NULL DEFAULT FALSE,
    condition_result    JSONB,
    result_hash         TEXT,
    result_sample       JSONB,
    attempt             INTEGER NOT NULL DEFAULT 1,
    error_code          TEXT,
    error_message       TEXT,
    instance_id         TEXT,
    idempotency_key     TEXT,
    cancel_requested_at TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL,
    created_by          UUID,
    version             BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX runs_queue_idx ON runs (status, available_at);
CREATE INDEX runs_report_idx ON runs (report_id, id);
CREATE UNIQUE INDEX runs_slot_idx ON runs (report_id, scheduled_for);
CREATE INDEX runs_idempotency_idx ON runs (report_id, idempotency_key);

CREATE TABLE instances (
    id           TEXT PRIMARY KEY NOT NULL,
    hostname     TEXT NOT NULL,
    version      TEXT NOT NULL,
    started_at   TIMESTAMPTZ NOT NULL,
    heartbeat_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE instances;
DROP TABLE runs;
DROP TABLE reports;
