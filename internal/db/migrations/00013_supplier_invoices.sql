-- +goose Up
-- +goose StatementBegin
-- Leverantorsfaktura (accounts payable). A supplier bill is a single cost line
-- (net + cost account + VAT treatment) plus the supplier and dates. On finalize
-- it auto-posts a balanced verifikat (series L) using the purchase VAT engine,
-- which for a foreign service self-assesses forvarvsmoms; on payment it settles
-- 2440 Leverantorsskulder against a bank account. Amounts are SEK ore.
CREATE TABLE supplier_invoices (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id              UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    counterparty_id         UUID NOT NULL REFERENCES counterparties(id),
    supplier_number         TEXT NOT NULL DEFAULT '',
    invoice_date            DATE,
    due_date                DATE,
    cost_account            TEXT NOT NULL,
    net_ore                 BIGINT NOT NULL,
    vat_code                TEXT NOT NULL,
    description             TEXT NOT NULL DEFAULT '',
    status                  TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'finalized', 'paid')),
    verification_id         UUID REFERENCES verifications(id),
    payment_verification_id UUID REFERENCES verifications(id),
    paid_at                 DATE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    finalized_at            TIMESTAMPTZ
);
CREATE INDEX idx_supplier_invoices_company ON supplier_invoices(company_id, status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS supplier_invoices;
-- +goose StatementEnd
