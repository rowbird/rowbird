-- +goose Up
CREATE TABLE notifications (
    id           TEXT PRIMARY KEY NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces (id),
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type         TEXT NOT NULL,
    severity     TEXT NOT NULL,
    title_key    TEXT NOT NULL,
    params       TEXT NOT NULL DEFAULT '{}',
    entity_type  TEXT NOT NULL DEFAULT '',
    entity_id    TEXT,
    group_key    TEXT NOT NULL DEFAULT '',
    count        INTEGER NOT NULL DEFAULT 1,
    first_at     TIMESTAMP NOT NULL,
    last_at      TIMESTAMP NOT NULL,
    read_at      TIMESTAMP,
    resolved_at  TIMESTAMP,
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL,
    created_by   TEXT,
    version      INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX notifications_user_idx ON notifications (workspace_id, user_id, read_at);
-- At most one open notification per user and group: new occurrences are counted on it.
CREATE UNIQUE INDEX notifications_open_group_idx ON notifications (workspace_id, user_id, group_key)
    WHERE resolved_at IS NULL AND group_key <> '';

-- +goose Down
DROP TABLE notifications;
