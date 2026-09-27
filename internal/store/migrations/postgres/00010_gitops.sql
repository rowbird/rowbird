-- +goose Up
-- Who manages a resource: '' (the UI and the API), 'gitops' (the configuration directory, read
-- only in the UI) or 'gitops_detached' (was managed, then detached by an admin; GitOps skips it).
ALTER TABLE connections ADD COLUMN managed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN managed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE queries ADD COLUMN managed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE reports ADD COLUMN managed_by TEXT NOT NULL DEFAULT '';
-- A GitOps report whose document left the directory: paused until it returns or is deleted.
ALTER TABLE reports ADD COLUMN gitops_orphan BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE reports DROP COLUMN gitops_orphan;
ALTER TABLE reports DROP COLUMN managed_by;
ALTER TABLE queries DROP COLUMN managed_by;
ALTER TABLE channels DROP COLUMN managed_by;
ALTER TABLE connections DROP COLUMN managed_by;
