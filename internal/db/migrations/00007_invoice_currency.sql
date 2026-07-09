-- +goose Up
-- +goose StatementBegin
-- rate_ppm: SEK per 1.00 of the invoice currency, in parts per million (6
-- decimals). SEK invoices use 1000000 (identity). Invoice line amounts stay in
-- the invoice currency; the verifikat is booked in SEK at this rate (Swedish
-- books are SEK). Kursdifferens on payment is a later feature.
ALTER TABLE invoices ADD COLUMN rate_ppm BIGINT NOT NULL DEFAULT 1000000;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE invoices DROP COLUMN IF EXISTS rate_ppm;
-- +goose StatementEnd
