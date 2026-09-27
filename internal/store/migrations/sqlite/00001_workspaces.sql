-- +goose Up
CREATE TABLE workspaces (
    id         TEXT PRIMARY KEY NOT NULL,
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    created_by TEXT,
    version    INTEGER NOT NULL DEFAULT 1
);

-- +goose Down
DROP TABLE workspaces;
