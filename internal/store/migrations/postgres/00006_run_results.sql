-- +goose Up
ALTER TABLE runs ADD COLUMN result_expires_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE runs DROP COLUMN result_expires_at;
