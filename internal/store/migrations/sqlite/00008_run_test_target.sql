-- +goose Up
ALTER TABLE runs ADD COLUMN test_target TEXT;

-- +goose Down
ALTER TABLE runs DROP COLUMN test_target;
