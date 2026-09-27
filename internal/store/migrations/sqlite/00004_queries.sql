-- +goose Up
CREATE TABLE queries (
    id                 TEXT PRIMARY KEY NOT NULL,
    workspace_id       TEXT NOT NULL REFERENCES workspaces (id),
    title              TEXT NOT NULL,
    slug               TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    connection_id      TEXT NOT NULL REFERENCES connections (id),
    current_version_id TEXT,
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL,
    created_by         TEXT,
    version            INTEGER NOT NULL DEFAULT 1,
    UNIQUE (workspace_id, slug)
);
CREATE INDEX queries_connection_idx ON queries (connection_id);

CREATE TABLE query_versions (
    id           TEXT PRIMARY KEY NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces (id),
    query_id     TEXT NOT NULL REFERENCES queries (id) ON DELETE CASCADE,
    number       INTEGER NOT NULL,
    sql          TEXT NOT NULL,
    params       TEXT NOT NULL DEFAULT '[]',
    note         TEXT NOT NULL DEFAULT '',
    restored_from INTEGER,
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL,
    created_by   TEXT,
    version      INTEGER NOT NULL DEFAULT 1,
    UNIQUE (query_id, number)
);

-- +goose Down
DROP TABLE query_versions;
DROP TABLE queries;
