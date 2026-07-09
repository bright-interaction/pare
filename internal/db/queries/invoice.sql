-- name: InsertInvoice :one
INSERT INTO invoices (company_id, counterparty_id, invoice_date, due_date, currency, rate_ppm, ocr, note_enc)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: InsertInvoiceLine :exec
INSERT INTO invoice_lines (invoice_id, line_no, description, quantity_milli, unit_price_ore, vat_code)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetInvoice :one
SELECT * FROM invoices WHERE id = $1;

-- name: AllocInvoiceNumber :one
-- Atomically allocate the next gap-free number for a company + year. First call
-- of a year returns 1; the row lock serializes concurrent finalizes.
INSERT INTO invoice_number_seq (company_id, year, next_no)
VALUES ($1, $2, 1)
ON CONFLICT (company_id, year)
DO UPDATE SET next_no = invoice_number_seq.next_no + 1
RETURNING next_no;

-- name: GetInvoiceByNumber :one
SELECT * FROM invoices WHERE company_id = $1 AND number = $2;

-- name: ListInvoiceLines :many
SELECT * FROM invoice_lines WHERE invoice_id = $1 ORDER BY line_no;

-- name: ListInvoices :many
SELECT * FROM invoices WHERE company_id = $1 ORDER BY created_at;

-- name: FinalizeInvoice :exec
UPDATE invoices
SET status = 'finalized', number = $2, invoice_date = $3, due_date = $4,
    verification_id = $5, finalized_at = now()
WHERE id = $1 AND company_id = $6 AND status = 'draft';

-- name: DeleteDraftInvoice :execrows
DELETE FROM invoices WHERE id = $1 AND company_id = $2 AND status = 'draft';

-- name: SetInvoiceCredits :exec
UPDATE invoices SET credits_invoice_id = $2 WHERE id = $1;

-- name: MarkInvoiceCancelled :execrows
UPDATE invoices SET status = 'cancelled' WHERE id = $1 AND company_id = $2 AND status = 'finalized';

-- name: MarkInvoiceCredited :execrows
UPDATE invoices SET status = 'credited' WHERE id = $1 AND company_id = $2 AND status IN ('finalized', 'paid');

-- name: AddInvoicePayment :exec
UPDATE invoices SET amount_paid_ore = amount_paid_ore + $2 WHERE id = $1 AND company_id = $3;

-- name: MarkInvoicePaid :execrows
UPDATE invoices
SET status = 'paid', paid_at = $2, payment_verification_id = $3
WHERE id = $1 AND company_id = $4 AND status = 'finalized';
