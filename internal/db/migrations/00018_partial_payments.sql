-- +goose Up
-- +goose StatementBegin
-- Partial payments: track the SEK settled so far so an invoice can be paid in
-- instalments. It stays 'finalized' until fully settled (then 'paid'). A final
-- payment within an öre tolerance of the balance closes it, booking the small
-- difference to 3740 (öres- och kronutjämning).
ALTER TABLE invoices ADD COLUMN amount_paid_ore BIGINT NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE invoices DROP COLUMN IF EXISTS amount_paid_ore;
-- +goose StatementEnd
