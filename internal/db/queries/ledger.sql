-- name: InsertCompany :one
INSERT INTO companies (name, orgnr, dek_wrapped, key_id, key_version)
VALUES ($1, $2, $3, $4, 1)
RETURNING *;

-- name: GetCompany :one
SELECT * FROM companies WHERE id = $1;

-- name: UpdateCompanyProfile :exec
UPDATE companies
SET name = $2, orgnr = $3, momsregnr = $4, address = $5, postal_code = $6,
    city = $7, bankgiro = $8, iban = $9, fskatt = $10
WHERE id = $1;

-- name: ListCompanies :many
SELECT * FROM companies ORDER BY created_at;

-- name: UpsertAccount :exec
INSERT INTO accounts (company_id, number, name, class, default_vat_code)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (company_id, number)
DO UPDATE SET name = EXCLUDED.name, class = EXCLUDED.class, default_vat_code = EXCLUDED.default_vat_code;

-- name: ListAccounts :many
SELECT * FROM accounts WHERE company_id = $1 ORDER BY number;

-- name: NextVerificationNumber :one
SELECT COALESCE(MAX(number), 0) + 1 AS next FROM verifications
WHERE company_id = $1 AND series = $2;

-- name: InsertVerification :one
INSERT INTO verifications (company_id, series, number, vdate, description, reversal_of, posted_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: InsertVerificationLine :exec
INSERT INTO verification_lines (verification_id, account, debit_ore, credit_ore, vat_code)
VALUES ($1, $2, $3, $4, $5);

-- name: ListVerifications :many
SELECT * FROM verifications WHERE company_id = $1 ORDER BY vdate, series, number;

-- name: ListLinesForCompany :many
SELECT l.verification_id, l.account, l.debit_ore, l.credit_ore, l.vat_code
FROM verification_lines l
JOIN verifications v ON v.id = l.verification_id
WHERE v.company_id = $1;

-- name: TrialBalance :many
SELECT l.account, SUM(l.debit_ore - l.credit_ore)::BIGINT AS net_ore
FROM verification_lines l
JOIN verifications v ON v.id = l.verification_id
WHERE v.company_id = $1
GROUP BY l.account
ORDER BY l.account;

-- name: TrialBalanceBetween :many
SELECT l.account, SUM(l.debit_ore - l.credit_ore)::BIGINT AS net_ore
FROM verification_lines l
JOIN verifications v ON v.id = l.verification_id
WHERE v.company_id = $1 AND v.vdate >= $2 AND v.vdate <= $3
GROUP BY l.account
ORDER BY l.account;

-- name: TrialBalanceAsOf :many
SELECT l.account, SUM(l.debit_ore - l.credit_ore)::BIGINT AS net_ore
FROM verification_lines l
JOIN verifications v ON v.id = l.verification_id
WHERE v.company_id = $1 AND v.vdate <= $2
GROUP BY l.account
ORDER BY l.account;

-- name: TrialBalanceBetweenExclSeries :many
-- Period balances excluding a voucher series (used for the resultaträkning so a
-- closed year still shows its real P&L, not the zeroed post-close figures).
SELECT l.account, SUM(l.debit_ore - l.credit_ore)::BIGINT AS net_ore
FROM verification_lines l
JOIN verifications v ON v.id = l.verification_id
WHERE v.company_id = $1 AND v.vdate >= $2 AND v.vdate <= $3 AND v.series <> $4
GROUP BY l.account
ORDER BY l.account;

-- name: InsertCounterparty :one
INSERT INTO counterparties
    (company_id, kind, name_enc, orgnr_enc, personnummer_enc, address_enc, iban_enc, email_enc)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetCounterparty :one
SELECT * FROM counterparties WHERE id = $1;

-- name: ListCounterparties :many
SELECT * FROM counterparties WHERE company_id = $1 ORDER BY created_at;

-- name: UpdateCounterparty :exec
UPDATE counterparties
SET kind = $3, name_enc = $4, orgnr_enc = $5, personnummer_enc = $6, address_enc = $7, iban_enc = $8, email_enc = $9
WHERE id = $1 AND company_id = $2 AND erased_at IS NULL;

-- name: EraseCounterparty :exec
UPDATE counterparties
SET name_enc = $3, orgnr_enc = '', personnummer_enc = '', address_enc = '', iban_enc = '', email_enc = '',
    erased_at = now()
WHERE id = $1 AND company_id = $2 AND erased_at IS NULL;

-- name: CountRetainedInvoices :one
SELECT count(*) FROM invoices
WHERE company_id = $1 AND counterparty_id = $2 AND status <> 'draft';
