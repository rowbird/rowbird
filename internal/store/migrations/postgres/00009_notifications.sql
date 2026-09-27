-- +goose Up
CREATE TABLE notifications (
    id           UUID PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES workspaces (id),
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type         TEXT NOT NULL,
    severity     TEXT NOT NULL,
    title_key    TEXT NOT NULL,
    params       JSONB NOT NULL DEFAULT '{}',
    entity_type  TEXT NOT NULL DEFAULT '',
    entity_id    UUID,
    group_key    TEXT NOT NULL DEFAULT '',
    count        INTEGER NOT NULL DEFAULT 1,
    first_at     TIMESTAMPTZ NOT NULL,
    last_at      TIMESTAMPTZ NOT NULL,
    read_at      TIMESTAMPTZ,
    resolved_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    created_by   UUID,
    version      BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX notifications_user_idx ON notifications (workspace_id, user_id, read_at);
-- At most one open notification per user and group: new occurrences are counted on it.
CREATE UNIQUE INDEX notifications_open_group_idx ON notifications (workspace_id, user_id, group_key)
    WHERE resolved_at IS NULL AND group_key <> '';

-- +goose Down
DROP TABLE notifications;
