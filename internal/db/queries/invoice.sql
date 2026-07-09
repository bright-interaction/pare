-- name: InsertInvoice :one
INSERT INTO invoices (company_id, counterparty_id, invoice_date, due_date, currency, rate_ppm, ocr, note_enc)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: InsertInvoiceLine :exec
INSERT INTO invoice_lines (invoice_id, line_no, description, quantity_milli, unit_price_ore, vat_code)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetInvoice :one
SELECT * FROM invoices WHERE id = $1;

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

-- name: MarkInvoicePaid :execrows
UPDATE invoices
SET status = 'paid', paid_at = $2, payment_verification_id = $3
WHERE id = $1 AND company_id = $4 AND status = 'finalized';
