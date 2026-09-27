-- +goose Up
CREATE TABLE channels (
    id               UUID PRIMARY KEY,
    workspace_id     UUID NOT NULL REFERENCES workspaces (id),
    name             TEXT NOT NULL,
    type             TEXT NOT NULL,
    config           JSONB NOT NULL DEFAULT '{}',
    secrets_enc      TEXT,
    status           TEXT NOT NULL DEFAULT 'unknown',
    last_success_at  TIMESTAMPTZ,
    last_failure_at  TIMESTAMPTZ,
    last_error       TEXT,
    is_system_mailer BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    created_by   UUID,
    version      BIGINT NOT NULL DEFAULT 1,
    UNIQUE (workspace_id, name)
);

CREATE TABLE deliveries (
    id                        UUID PRIMARY KEY,
    workspace_id              UUID NOT NULL REFERENCES workspaces (id),
    report_id                 UUID NOT NULL REFERENCES reports (id) ON DELETE CASCADE,
    channel_id                UUID NOT NULL REFERENCES channels (id),
    position                  INTEGER NOT NULL DEFAULT 0,
    enabled                   BOOLEAN NOT NULL DEFAULT TRUE,
    mode                      TEXT NOT NULL,
    formats                   JSONB NOT NULL DEFAULT '[]',
    inline_row_limit          INTEGER NOT NULL DEFAULT 20,
    include_inline_with_files BOOLEAN NOT NULL DEFAULT TRUE,
    link_expires_seconds      INTEGER NOT NULL DEFAULT 604800,
    link_require_login        BOOLEAN NOT NULL DEFAULT FALSE,
    options                   JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    created_by   UUID,
    version      BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX deliveries_report_idx ON deliveries (report_id, position);
CREATE INDEX deliveries_channel_idx ON deliveries (channel_id);

CREATE TABLE artifacts (
    id              UUID PRIMARY KEY,
    workspace_id    UUID NOT NULL REFERENCES workspaces (id),
    run_id          UUID NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    format          TEXT NOT NULL,
    content_type    TEXT NOT NULL,
    file_name       TEXT NOT NULL,
    storage_backend TEXT NOT NULL,
    storage_key     TEXT NOT NULL,
    size_bytes      BIGINT NOT NULL,
    sha256          TEXT NOT NULL,
    expires_at      TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    created_by   UUID,
    version      BIGINT NOT NULL DEFAULT 1,
    UNIQUE (run_id, format)
);

CREATE TABLE delivery_attempts (
    id              UUID PRIMARY KEY,
    workspace_id    UUID NOT NULL REFERENCES workspaces (id),
    run_id          UUID NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    delivery_id     UUID REFERENCES deliveries (id) ON DELETE SET NULL,
    channel_id      UUID REFERENCES channels (id) ON DELETE SET NULL,
    status          TEXT NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error_code TEXT,
    last_error      TEXT,
    sent_at         TIMESTAMPTZ,
    meta            JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    created_by   UUID,
    version      BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX delivery_attempts_run_idx ON delivery_attempts (run_id);
CREATE INDEX delivery_attempts_channel_idx ON delivery_attempts (channel_id, status);

CREATE TABLE shared_links (
    id               UUID PRIMARY KEY,
    workspace_id     UUID NOT NULL REFERENCES workspaces (id),
    artifact_id      UUID NOT NULL REFERENCES artifacts (id) ON DELETE CASCADE,
    run_id           UUID NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    delivery_id      UUID REFERENCES deliveries (id) ON DELETE SET NULL,
    token_hash       TEXT NOT NULL UNIQUE,
    expires_at       TIMESTAMPTZ NOT NULL,
    require_login    BOOLEAN NOT NULL DEFAULT FALSE,
    revoked_at       TIMESTAMPTZ,
    revoked_by       UUID,
    download_count   INTEGER NOT NULL DEFAULT 0,
    last_download_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    created_by   UUID,
    version      BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX shared_links_run_idx ON shared_links (run_id);

CREATE TABLE link_downloads (
    id           UUID PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES workspaces (id),
    link_id      UUID NOT NULL REFERENCES shared_links (id) ON DELETE CASCADE,
    user_id      UUID,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    created_by   UUID,
    version      BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX link_downloads_link_idx ON link_downloads (link_id);

-- +goose Down
DROP TABLE link_downloads;
DROP TABLE shared_links;
DROP TABLE delivery_attempts;
DROP TABLE artifacts;
DROP TABLE deliveries;
DROP TABLE channels;
