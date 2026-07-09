-- +goose Up
-- +goose StatementBegin
-- Tamper-evidence for the audit log: each entry stores the previous entry's hash
-- and its own hash over (prev_hash + content fields). Editing or deleting any
-- entry breaks the chain, which VerifyAuditChain detects. The verifikat
-- themselves are already DB-immutable (00002 triggers); this hardens the trail.
ALTER TABLE audit_log ADD COLUMN prev_hash  TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN entry_hash TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE audit_log DROP COLUMN IF EXISTS entry_hash;
ALTER TABLE audit_log DROP COLUMN IF EXISTS prev_hash;
-- +goose StatementEnd
