-- +goose Up
-- +goose StatementBegin
CREATE TABLE invoices (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id      UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    counterparty_id UUID NOT NULL REFERENCES counterparties(id),
    number          TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft', 'finalized', 'paid', 'cancelled')),
    invoice_date    DATE,
    due_date        DATE,
    currency        TEXT NOT NULL DEFAULT 'SEK',
    ocr             TEXT NOT NULL DEFAULT '',
    note_enc        TEXT NOT NULL DEFAULT '',
    verification_id UUID REFERENCES verifications(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finalized_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_invoices_number ON invoices (company_id, number) WHERE number <> '';
CREATE INDEX idx_invoices_company ON invoices (company_id, status);
-- +goose StatementEnd

-- +goose StatementBegin
-- quantity_milli: quantity * 1000 (3 decimals) so 7.5h = 7500. unit_price_ore:
-- net unit price in öre. Amounts stay integer; VAT is computed by internal/moms.
CREATE TABLE invoice_lines (
    id             BIGSERIAL PRIMARY KEY,
    invoice_id     UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    line_no        INT NOT NULL,
    description    TEXT NOT NULL,
    quantity_milli BIGINT NOT NULL DEFAULT 1000,
    unit_price_ore BIGINT NOT NULL,
    vat_code       TEXT NOT NULL DEFAULT 'SE25'
);
CREATE INDEX idx_invoice_lines_invoice ON invoice_lines (invoice_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS invoice_lines;
DROP TABLE IF EXISTS invoices;
-- +goose StatementEnd
