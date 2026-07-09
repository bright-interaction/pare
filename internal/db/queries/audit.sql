-- name: SetLockedThrough :exec
UPDATE companies SET locked_through = $2 WHERE id = $1;

-- name: InsertAuditLog :exec
INSERT INTO audit_log (company_id, actor, actor_detail, action, target_type, target_id, detail, prev_hash, entry_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetLastAuditHash :one
SELECT entry_hash FROM audit_log WHERE company_id = $1 ORDER BY id DESC LIMIT 1;

-- name: ListAuditLog :many
SELECT * FROM audit_log WHERE company_id = $1 ORDER BY at DESC, id DESC LIMIT $2;

-- name: ListAuditChain :many
SELECT id, actor, actor_detail, action, target_type, target_id, detail, prev_hash, entry_hash
FROM audit_log WHERE company_id = $1 ORDER BY id ASC;

-- name: GetVerification :one
SELECT * FROM verifications WHERE id = $1;

-- name: ListVerificationLinesByVerification :many
SELECT account, debit_ore, credit_ore, vat_code
FROM verification_lines WHERE verification_id = $1 ORDER BY id;
