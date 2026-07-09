-- +goose Up
-- +goose StatementBegin
-- GDPR erasure (art. 17) tombstone. Identity ciphertext columns are overwritten
-- in place and erased_at is stamped; the row and its ledger links survive so the
-- immutable verifikat stay intact. Erasure is only permitted when the
-- counterparty is not referenced by retained accounting records (see store),
-- reconciling the right to erasure with the 7-year retention duty in
-- bokföringslagen (GDPR art. 17(3)(b): legal obligation).
ALTER TABLE counterparties ADD COLUMN erased_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE counterparties DROP COLUMN IF EXISTS erased_at;
-- +goose StatementEnd
