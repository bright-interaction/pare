-- +goose Up
-- +goose StatementBegin
-- Make the audit hash chain fork-proof, not just tamper-detectable: two
-- transactions that both chain a new entry off the same previous hash would
-- collide here, so the second fails instead of silently branching. (The genesis
-- entry has prev_hash = '', unique per company.)
CREATE UNIQUE INDEX idx_audit_prev_hash ON audit_log (company_id, prev_hash);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_audit_prev_hash;
-- +goose StatementEnd
