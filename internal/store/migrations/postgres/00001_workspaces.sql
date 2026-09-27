-- +goose Up
CREATE TABLE workspaces (
    id         UUID PRIMARY KEY,
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    created_by UUID,
    version    BIGINT NOT NULL DEFAULT 1
);

-- +goose Down
DROP TABLE workspaces;
