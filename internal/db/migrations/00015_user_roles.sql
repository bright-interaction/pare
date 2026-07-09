-- +goose Up
-- +goose StatementBegin
-- Roles for the accountant handoff: 'owner' can do everything; 'viewer' (the
-- revisor) is read-only. Existing users default to owner.
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'owner'
    CHECK (role IN ('owner', 'viewer'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS role;
-- +goose StatementEnd
