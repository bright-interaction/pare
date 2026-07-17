-- +goose Up
-- +goose StatementBegin
-- Add a transient 'booking' state so a bank txn can be atomically claimed before
-- its payment verifikat is posted. Without this claim, two concurrent bookings of
-- the same credit (double-click, or MCP racing the web UI) both read 'unmatched'
-- and both post a payment, double-booking the bank line.
ALTER TABLE bank_transactions DROP CONSTRAINT IF EXISTS bank_transactions_status_check;
ALTER TABLE bank_transactions ADD CONSTRAINT bank_transactions_status_check
    CHECK (status IN ('unmatched', 'booking', 'booked', 'ignored'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Release any stuck claims before narrowing the constraint back.
UPDATE bank_transactions SET status = 'unmatched' WHERE status = 'booking';
ALTER TABLE bank_transactions DROP CONSTRAINT IF EXISTS bank_transactions_status_check;
ALTER TABLE bank_transactions ADD CONSTRAINT bank_transactions_status_check
    CHECK (status IN ('unmatched', 'booked', 'ignored'));
-- +goose StatementEnd
