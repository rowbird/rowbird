-- +goose Up
CREATE TABLE users (
    id                   TEXT PRIMARY KEY NOT NULL,
    email                TEXT NOT NULL UNIQUE,
    name                 TEXT NOT NULL,
    password_hash        TEXT,
    locale               TEXT NOT NULL,
    theme                TEXT NOT NULL DEFAULT 'system',
    totp_secret_enc      TEXT,
    totp_enabled         INTEGER NOT NULL DEFAULT 0,
    totp_last_step       INTEGER NOT NULL DEFAULT 0,
    recovery_codes       TEXT,
    must_change_password INTEGER NOT NULL DEFAULT 0,
    failed_logins        INTEGER NOT NULL DEFAULT 0,
    locked_until         TIMESTAMP,
    disabled_at          TIMESTAMP,
    last_login_at        TIMESTAMP,
    oidc_subject         TEXT,
    oidc_issuer          TEXT,
    created_at           TIMESTAMP NOT NULL,
    updated_at           TIMESTAMP NOT NULL,
    created_by           TEXT,
    version              INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE workspace_members (
    id           TEXT PRIMARY KEY NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces (id),
    user_id      TEXT NOT NULL REFERENCES users (id),
    role         TEXT NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL,
    created_by   TEXT,
    version      INTEGER NOT NULL DEFAULT 1,
    UNIQUE (workspace_id, user_id)
);
CREATE INDEX workspace_members_user_idx ON workspace_members (user_id);

CREATE TABLE sessions (
    id           TEXT PRIMARY KEY NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces (id),
    user_id      TEXT NOT NULL REFERENCES users (id),
    token_hash   TEXT NOT NULL UNIQUE,
    expires_at   TIMESTAMP NOT NULL,
    last_seen_at TIMESTAMP NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    revoked_at   TIMESTAMP,
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL,
    created_by   TEXT,
    version      INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE login_challenges (
    id         TEXT PRIMARY KEY NOT NULL,
    user_id    TEXT NOT NULL REFERENCES users (id),
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMP NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    created_by TEXT,
    version    INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE api_keys (
    id           TEXT PRIMARY KEY NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces (id),
    user_id      TEXT NOT NULL REFERENCES users (id),
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    scopes       TEXT NOT NULL,
    expires_at   TIMESTAMP,
    last_used_at TIMESTAMP,
    revoked_at   TIMESTAMP,
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL,
    created_by   TEXT,
    version      INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE security_events (
    id            TEXT PRIMARY KEY NOT NULL,
    workspace_id  TEXT NOT NULL REFERENCES workspaces (id),
    actor_user_id TEXT,
    type          TEXT NOT NULL,
    ip            TEXT NOT NULL DEFAULT '',
    meta          TEXT,
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,
    created_by    TEXT,
    version       INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX security_events_type_idx ON security_events (workspace_id, type);

CREATE TABLE settings (
    id           TEXT PRIMARY KEY NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces (id),
    key          TEXT NOT NULL,
    value        TEXT NOT NULL,
    secret       INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL,
    created_by   TEXT,
    version      INTEGER NOT NULL DEFAULT 1,
    UNIQUE (workspace_id, key)
);

-- +goose Down
DROP TABLE settings;
DROP TABLE security_events;
DROP TABLE api_keys;
DROP TABLE login_challenges;
DROP TABLE sessions;
DROP TABLE workspace_members;
DROP TABLE users;
