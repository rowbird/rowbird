-- +goose Up
-- "Forgot password" links: one-use tokens stored as a hash, valid 30 minutes. A new request
-- replaces the user's earlier ones.
CREATE TABLE password_resets (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMP NOT NULL,
    used_at    TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    created_by TEXT,
    version    BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX password_resets_user_idx ON password_resets (user_id);

-- +goose Down
DROP TABLE password_resets;
