-- +goose Up
-- The id of the master key each instance encrypts with, so `rowbird keys rotate` can tell whether
-- the running instances already read values encrypted with the new key.
ALTER TABLE instances ADD COLUMN key_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE instances DROP COLUMN key_id;
