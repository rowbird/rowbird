-- +goose Up
-- WebAuthn credentials. A passkey belongs to a user, like the TOTP secret, and counts as a second
-- factor; credential holds the whole credential record as JSON.
CREATE TABLE passkeys (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    credential_id TEXT NOT NULL UNIQUE,
    credential    TEXT NOT NULL,
    sign_count    BIGINT NOT NULL DEFAULT 0,
    last_used_at  TIMESTAMP,
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,
    created_by    TEXT,
    version       BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX passkeys_user_idx ON passkeys (user_id);

-- The server side of a WebAuthn ceremony between its options and its answer: single use, short
-- lived, looked up by the hash of the token given to the browser.
CREATE TABLE webauthn_challenges (
    id         TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL,
    purpose    TEXT NOT NULL,
    user_id    TEXT REFERENCES users (id) ON DELETE CASCADE,
    data       TEXT NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    created_by TEXT,
    version    BIGINT NOT NULL DEFAULT 1,
    UNIQUE (token_hash, purpose)
);

-- +goose Down
DROP TABLE webauthn_challenges;
DROP TABLE passkeys;
