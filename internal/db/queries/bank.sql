-- name: InsertBankTxn :exec
INSERT INTO bank_transactions (company_id, txn_date, amount_ore, text_enc, ref, bank_account, fingerprint)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (company_id, fingerprint) DO NOTHING;

-- name: ListBankTxns :many
SELECT * FROM bank_transactions WHERE company_id = $1 ORDER BY txn_date DESC, created_at DESC;

-- name: GetBankTxn :one
SELECT * FROM bank_transactions WHERE id = $1;

-- name: ClaimBankTxn :execrows
-- Atomically move an unmatched txn to the transient 'booking' state so a
-- concurrent book (double-click / MCP racing the web UI) cannot also settle the
-- same bank credit. Exactly one caller wins; the loser gets 0 rows.
UPDATE bank_transactions SET status = 'booking'
WHERE id = $1 AND company_id = $2 AND status = 'unmatched';

-- name: UnclaimBankTxn :execrows
-- Release a claim back to 'unmatched' when booking fails before it commits (e.g.
-- wrong invoice), so the operator can retry.
UPDATE bank_transactions SET status = 'unmatched'
WHERE id = $1 AND company_id = $2 AND status = 'booking';

-- name: MarkBankTxnBooked :execrows
UPDATE bank_transactions
SET status = 'booked', verification_id = $3, matched_invoice_id = $4
WHERE id = $1 AND company_id = $2 AND status IN ('unmatched', 'booking');

-- name: MarkBankTxnIgnored :execrows
UPDATE bank_transactions SET status = 'ignored'
WHERE id = $1 AND company_id = $2 AND status = 'unmatched';
