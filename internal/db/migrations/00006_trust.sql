-- +goose Up
-- +goose StatementBegin
-- Period lock: postings dated on or before locked_through are rejected. Locking
-- advances it to a period end; unlocking moves it back and is logged. The MCP
-- never exposes unlock.
ALTER TABLE companies ADD COLUMN locked_through DATE;
-- +goose StatementEnd

-- +goose StatementBegin
-- Audit log: the trust layer. Every write records who did it (user / ai /
-- system), what, and against which target. detail stays free of counterparty
-- identities (references + amounts only) so it can be read back through the MCP
-- without leaking PII.
CREATE TABLE audit_log (
    id           BIGSERIAL PRIMARY KEY,
    company_id   UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    actor        TEXT NOT NULL CHECK (actor IN ('user', 'ai', 'system')),
    actor_detail TEXT NOT NULL DEFAULT '',
    action       TEXT NOT NULL,
    target_type  TEXT NOT NULL DEFAULT '',
    target_id    TEXT NOT NULL DEFAULT '',
    detail       TEXT NOT NULL DEFAULT '',
    at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_company_at ON audit_log (company_id, at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_log;
ALTER TABLE companies DROP COLUMN IF EXISTS locked_through;
-- +goose StatementEnd
