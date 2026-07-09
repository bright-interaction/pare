-- +goose Up
-- +goose StatementBegin
-- Kreditfaktura: a credit note is a new invoice whose lines negate an original;
-- credits_invoice_id links it back so its PDF can reference the original number
-- and the original can be marked 'cancelled' (reversed by a credit note).
ALTER TABLE invoices ADD COLUMN credits_invoice_id UUID REFERENCES invoices(id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE invoices DROP COLUMN IF EXISTS credits_invoice_id;
-- +goose StatementEnd
