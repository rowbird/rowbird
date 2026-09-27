-- +goose Up
-- How a session signed in: password (with or without a second factor), passkey or oidc. OIDC
-- sessions leave the second factor to the identity provider.
ALTER TABLE sessions ADD COLUMN auth_method TEXT NOT NULL DEFAULT 'password';
-- One Rowbird user per identity at a provider.
CREATE UNIQUE INDEX users_oidc_identity_idx ON users (oidc_issuer, oidc_subject) WHERE oidc_subject IS NOT NULL;

-- +goose Down
DROP INDEX users_oidc_identity_idx;
ALTER TABLE sessions DROP COLUMN auth_method;
