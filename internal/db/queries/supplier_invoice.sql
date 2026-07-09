-- name: InsertSupplierInvoice :one
INSERT INTO supplier_invoices
    (company_id, counterparty_id, supplier_number, invoice_date, due_date, cost_account, net_ore, vat_code, description)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetSupplierInvoice :one
SELECT * FROM supplier_invoices WHERE id = $1;

-- name: ListSupplierInvoices :many
SELECT * FROM supplier_invoices WHERE company_id = $1 ORDER BY created_at DESC;

-- name: FinalizeSupplierInvoice :execrows
UPDATE supplier_invoices
SET status = 'finalized', verification_id = $2, finalized_at = now()
WHERE id = $1 AND company_id = $3 AND status = 'draft';

-- name: MarkSupplierInvoicePaid :execrows
UPDATE supplier_invoices
SET status = 'paid', paid_at = $2, payment_verification_id = $3
WHERE id = $1 AND company_id = $4 AND status = 'finalized';
