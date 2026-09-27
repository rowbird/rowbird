-- +goose Up
CREATE TABLE connections (
    id                    TEXT PRIMARY KEY NOT NULL,
    workspace_id          TEXT NOT NULL REFERENCES workspaces (id),
    name                  TEXT NOT NULL,
    driver                TEXT NOT NULL,
    config                TEXT NOT NULL,
    secrets_enc           TEXT,
    query_timeout_seconds INTEGER NOT NULL DEFAULT 60,
    max_rows              INTEGER NOT NULL DEFAULT 100000,
    allow_multi_statement INTEGER NOT NULL DEFAULT 0,
    ai_excluded_tables    TEXT NOT NULL DEFAULT '[]',
    schema_cache          TEXT,
    schema_cached_at      TIMESTAMP,
    has_write_permission  INTEGER,
    status                TEXT NOT NULL DEFAULT 'unknown',
    server_version        TEXT NOT NULL DEFAULT '',
    last_checked_at       TIMESTAMP,
    last_error            TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMP NOT NULL,
    updated_at            TIMESTAMP NOT NULL,
    created_by            TEXT,
    version               INTEGER NOT NULL DEFAULT 1,
    UNIQUE (workspace_id, name)
);

-- +goose Down
DROP TABLE connections;
