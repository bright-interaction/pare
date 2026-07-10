-- +goose Up
-- +goose StatementBegin
-- Receipts and supporting documents (verifikationsunderlag). The file bytes are
-- envelope-encrypted at rest (DEK.EncryptBytes) - a receipt is räkenskaps-
-- information full of PII, so a DB/blob dump leaks nothing. Optionally linked to
-- a supplier invoice or a verifikat. The plaintext file never leaves Pare (not
-- exposed over the MCP/LLM boundary).
CREATE TABLE documents (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id          UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    filename            TEXT NOT NULL,
    mime                TEXT NOT NULL DEFAULT '',
    byte_size           BIGINT NOT NULL DEFAULT 0,
    content_enc         BYTEA NOT NULL,
    sha256              TEXT NOT NULL DEFAULT '',
    note                TEXT NOT NULL DEFAULT '',
    supplier_invoice_id UUID REFERENCES supplier_invoices(id) ON DELETE SET NULL,
    verification_id     UUID REFERENCES verifications(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_documents_company ON documents (company_id, created_at DESC);
CREATE INDEX idx_documents_supplier ON documents (supplier_invoice_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS documents;
-- +goose StatementEnd
