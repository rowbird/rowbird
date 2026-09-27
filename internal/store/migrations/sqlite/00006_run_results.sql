-- +goose Up
ALTER TABLE runs ADD COLUMN result_expires_at TIMESTAMP;

-- +goose Down
ALTER TABLE runs DROP COLUMN result_expires_at;
