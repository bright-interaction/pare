-- +goose Up
-- +goose StatementBegin

-- One company per row. BI runs a single company now; multi-company is a pro
-- overlay concern. Each company owns a wrapped data-encryption key (DEK); the
-- DEK is unwrapped in memory with the KEK (PARE_MASTER_KEY) to encrypt/decrypt
-- the *_enc identity columns below.
CREATE TABLE companies (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    orgnr       TEXT NOT NULL DEFAULT '',
    dek_wrapped TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Chart of accounts (BAS kontoplan). class is the kontoklass (1..8), derived
-- from the first digit; stored for cheap reporting joins.
CREATE TABLE accounts (
    company_id       UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    number           TEXT NOT NULL,
    name             TEXT NOT NULL,
    class            SMALLINT NOT NULL,
    default_vat_code TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (company_id, number)
);

CREATE TABLE fiscal_years (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    label      TEXT NOT NULL,
    starts_on  DATE NOT NULL,
    ends_on    DATE NOT NULL
);

-- Verifikat. Append-only: once posted_at is set the row and its lines are
-- never updated or deleted; corrections are new verifications with
-- reversal_of pointing back (rättelseverifikat), per Bokföringslagen. A
-- guard trigger blocks UPDATE/DELETE on posted rows in a later migration.
CREATE TABLE verifications (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id  UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    series      TEXT NOT NULL,
    number      INTEGER NOT NULL,
    vdate       DATE NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    reversal_of UUID REFERENCES verifications(id),
    posted_at   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, series, number)
);

-- Amounts are BIGINT öre (minor units); never floating point.
CREATE TABLE verification_lines (
    id              BIGSERIAL PRIMARY KEY,
    verification_id UUID NOT NULL REFERENCES verifications(id) ON DELETE CASCADE,
    account         TEXT NOT NULL,
    debit_ore       BIGINT NOT NULL DEFAULT 0 CHECK (debit_ore >= 0),
    credit_ore      BIGINT NOT NULL DEFAULT 0 CHECK (credit_ore >= 0),
    vat_code        TEXT NOT NULL DEFAULT '',
    CHECK ((debit_ore = 0) <> (credit_ore = 0))
);
CREATE INDEX idx_vlines_verification ON verification_lines(verification_id);
CREATE INDEX idx_vlines_account ON verification_lines(account);

-- Counterparties (customers/suppliers). Identity columns hold base64
-- envelope ciphertext (crypto.DEK.EncryptField), never plaintext. Amounts and
-- links live on invoices/verifications so they stay queryable.
CREATE TABLE counterparties (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id        UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL CHECK (kind IN ('customer', 'supplier')),
    name_enc          TEXT NOT NULL,
    orgnr_enc         TEXT NOT NULL DEFAULT '',
    personnummer_enc  TEXT NOT NULL DEFAULT '',
    address_enc       TEXT NOT NULL DEFAULT '',
    iban_enc          TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_counterparties_company ON counterparties(company_id, kind);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS counterparties;
DROP TABLE IF EXISTS verification_lines;
DROP TABLE IF EXISTS verifications;
DROP TABLE IF EXISTS fiscal_years;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS companies;
-- +goose StatementEnd
