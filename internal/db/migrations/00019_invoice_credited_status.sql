-- +goose Up
-- +goose StatementBegin
-- A credited invoice is distinct from a cancelled one: it was a valid issued
-- document that a kreditfaktura later reversed (a paid invoice can be credited
-- too = a refund). Keep 'cancelled' for genuinely voided drafts/invoices.
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_status_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_status_check
    CHECK (status IN ('draft', 'finalized', 'paid', 'cancelled', 'credited'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_status_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_status_check
    CHECK (status IN ('draft', 'finalized', 'paid', 'cancelled'));
-- +goose StatementEnd
