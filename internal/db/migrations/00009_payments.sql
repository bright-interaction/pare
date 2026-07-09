-- +goose Up
-- +goose StatementBegin
-- Payment settles a finalized invoice: books debit bank / credit Kundfordringar
-- (1510), with any exchange difference to 3960 (gain) / 7960 (loss) for a
-- foreign-currency invoice. paid_at + the settlement verifikat close the
-- invoice lifecycle.
ALTER TABLE invoices ADD COLUMN paid_at DATE;
ALTER TABLE invoices ADD COLUMN payment_verification_id UUID REFERENCES verifications(id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE invoices DROP COLUMN IF EXISTS payment_verification_id;
ALTER TABLE invoices DROP COLUMN IF EXISTS paid_at;
-- +goose StatementEnd
