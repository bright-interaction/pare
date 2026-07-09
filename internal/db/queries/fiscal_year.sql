-- name: InsertFiscalYear :one
INSERT INTO fiscal_years (company_id, label, starts_on, ends_on)
VALUES ($1, $2, $3, $4)
ON CONFLICT (company_id, starts_on) DO NOTHING
RETURNING *;

-- name: ListFiscalYears :many
SELECT * FROM fiscal_years WHERE company_id = $1 ORDER BY starts_on;

-- name: GetFiscalYear :one
SELECT * FROM fiscal_years WHERE id = $1;

-- name: CloseFiscalYear :execrows
UPDATE fiscal_years SET closed_at = now()
WHERE id = $1 AND company_id = $2 AND closed_at IS NULL;

-- name: CountFiscalYears :one
SELECT count(*) FROM fiscal_years WHERE company_id = $1;
