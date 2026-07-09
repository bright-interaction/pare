-- +goose Up
-- +goose StatementBegin
-- Counterparty email (for sending invoices + reminders). Encrypted at rest like
-- the other identity fields (email is personal data).
ALTER TABLE counterparties ADD COLUMN email_enc TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE counterparties DROP COLUMN IF EXISTS email_enc;
-- +goose StatementEnd
