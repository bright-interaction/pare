-- name: InsertBankTxn :exec
INSERT INTO bank_transactions (company_id, txn_date, amount_ore, text_enc, ref, bank_account, fingerprint)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (company_id, fingerprint) DO NOTHING;

-- name: ListBankTxns :many
SELECT * FROM bank_transactions WHERE company_id = $1 ORDER BY txn_date DESC, created_at DESC;

-- name: GetBankTxn :one
SELECT * FROM bank_transactions WHERE id = $1;

-- name: MarkBankTxnBooked :execrows
UPDATE bank_transactions
SET status = 'booked', verification_id = $3, matched_invoice_id = $4
WHERE id = $1 AND company_id = $2 AND status = 'unmatched';

-- name: MarkBankTxnIgnored :execrows
UPDATE bank_transactions SET status = 'ignored'
WHERE id = $1 AND company_id = $2 AND status = 'unmatched';
