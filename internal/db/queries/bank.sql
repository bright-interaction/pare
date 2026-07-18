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

-- name: AnonymizeNonRetainedBankTxns :execrows
-- Blank the free-text PII (payer name / message, text_enc) of bank lines that
-- were never booked (unmatched or ignored, no verifikat, no matched invoice) and
-- are older than the cutoff. These are not räkenskapsinformation, so the identity
-- need not be retained; the cutoff avoids wiping a line the user might still
-- reconcile. The row + fingerprint stay so re-import is still idempotent.
UPDATE bank_transactions SET text_enc = ''
WHERE company_id = $1 AND status IN ('unmatched', 'ignored')
  AND verification_id IS NULL AND matched_invoice_id IS NULL
  AND text_enc <> '' AND created_at < $2;
