-- +goose Up
CREATE TABLE connections (
    id                    UUID PRIMARY KEY,
    workspace_id          UUID NOT NULL REFERENCES workspaces (id),
    name                  TEXT NOT NULL,
    driver                TEXT NOT NULL,
    config                JSONB NOT NULL,
    secrets_enc           TEXT,
    query_timeout_seconds INTEGER NOT NULL DEFAULT 60,
    max_rows              INTEGER NOT NULL DEFAULT 100000,
    allow_multi_statement BOOLEAN NOT NULL DEFAULT FALSE,
    ai_excluded_tables    JSONB NOT NULL DEFAULT '[]',
    schema_cache          JSONB,
    schema_cached_at      TIMESTAMPTZ,
    has_write_permission  BOOLEAN,
    status                TEXT NOT NULL DEFAULT 'unknown',
    server_version        TEXT NOT NULL DEFAULT '',
    last_checked_at       TIMESTAMPTZ,
    last_error            TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL,
    updated_at            TIMESTAMPTZ NOT NULL,
    created_by            UUID,
    version               BIGINT NOT NULL DEFAULT 1,
    UNIQUE (workspace_id, name)
);

-- +goose Down
DROP TABLE connections;
