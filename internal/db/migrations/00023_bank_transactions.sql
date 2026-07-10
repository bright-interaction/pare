-- +goose Up
-- +goose StatementBegin
-- Imported bank transactions for reconciliation. amount_ore is signed (credit +,
-- debit -). The free text (payer name / message) is PII, so it is encrypted at
-- rest (text_enc); ref (structured OCR) and the dedup fingerprint stay clear.
-- fingerprint = hash(date|amount|text|ref) over the PLAINTEXT so re-importing the
-- same statement is idempotent (UNIQUE + ON CONFLICT DO NOTHING). The same shape
-- is what a live PSD2 feed would produce.
CREATE TABLE bank_transactions (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id         UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    txn_date           DATE NOT NULL,
    amount_ore         BIGINT NOT NULL,
    text_enc           TEXT NOT NULL DEFAULT '',
    ref                TEXT NOT NULL DEFAULT '',
    bank_account       TEXT NOT NULL DEFAULT '1930',
    fingerprint        TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'unmatched'
                           CHECK (status IN ('unmatched', 'booked', 'ignored')),
    matched_invoice_id UUID REFERENCES invoices(id),
    verification_id    UUID REFERENCES verifications(id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, fingerprint)
);
CREATE INDEX idx_bank_txn_company ON bank_transactions (company_id, txn_date DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS bank_transactions;
-- +goose StatementEnd
