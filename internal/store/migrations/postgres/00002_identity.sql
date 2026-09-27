-- +goose Up
CREATE TABLE users (
    id                   UUID PRIMARY KEY,
    email                TEXT NOT NULL UNIQUE,
    name                 TEXT NOT NULL,
    password_hash        TEXT,
    locale               TEXT NOT NULL,
    theme                TEXT NOT NULL DEFAULT 'system',
    totp_secret_enc      TEXT,
    totp_enabled         BOOLEAN NOT NULL DEFAULT FALSE,
    totp_last_step       BIGINT NOT NULL DEFAULT 0,
    recovery_codes       JSONB,
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    failed_logins        INTEGER NOT NULL DEFAULT 0,
    locked_until         TIMESTAMPTZ,
    disabled_at          TIMESTAMPTZ,
    last_login_at        TIMESTAMPTZ,
    oidc_subject         TEXT,
    oidc_issuer          TEXT,
    created_at           TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL,
    created_by           UUID,
    version              BIGINT NOT NULL DEFAULT 1
);

CREATE TABLE workspace_members (
    id           UUID PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES workspaces (id),
    user_id      UUID NOT NULL REFERENCES users (id),
    role         TEXT NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    created_by   UUID,
    version      BIGINT NOT NULL DEFAULT 1,
    UNIQUE (workspace_id, user_id)
);
CREATE INDEX workspace_members_user_idx ON workspace_members (user_id);

CREATE TABLE sessions (
    id           UUID PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES workspaces (id),
    user_id      UUID NOT NULL REFERENCES users (id),
    token_hash   TEXT NOT NULL UNIQUE,
    expires_at   TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    created_by   UUID,
    version      BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE login_challenges (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id),
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    created_by UUID,
    version    BIGINT NOT NULL DEFAULT 1
);

CREATE TABLE api_keys (
    id           UUID PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES workspaces (id),
    user_id      UUID NOT NULL REFERENCES users (id),
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    scopes       JSONB NOT NULL,
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    created_by   UUID,
    version      BIGINT NOT NULL DEFAULT 1
);

CREATE TABLE security_events (
    id            UUID PRIMARY KEY,
    workspace_id  UUID NOT NULL REFERENCES workspaces (id),
    actor_user_id UUID,
    type          TEXT NOT NULL,
    ip            TEXT NOT NULL DEFAULT '',
    meta          JSONB,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    created_by    UUID,
    version       BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX security_events_type_idx ON security_events (workspace_id, type);

CREATE TABLE settings (
    id           UUID PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES workspaces (id),
    key          TEXT NOT NULL,
    value        JSONB NOT NULL,
    secret       BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    created_by   UUID,
    version      BIGINT NOT NULL DEFAULT 1,
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
