-- +goose Up
-- +goose StatementBegin
-- Seller profile needed to print a legally valid Swedish faktura (ML 2023:200 /
-- EU VAT Dir art. 226): the seller VAT number, address, a payee account so the
-- invoice can actually be paid, and the F-skatt status line. These are the
-- company's OWN public business identifiers (not counterparty PII), so they stay
-- cleartext like name/orgnr already are.
ALTER TABLE companies ADD COLUMN momsregnr   TEXT NOT NULL DEFAULT '';
ALTER TABLE companies ADD COLUMN address     TEXT NOT NULL DEFAULT '';
ALTER TABLE companies ADD COLUMN postal_code TEXT NOT NULL DEFAULT '';
ALTER TABLE companies ADD COLUMN city        TEXT NOT NULL DEFAULT '';
ALTER TABLE companies ADD COLUMN bankgiro    TEXT NOT NULL DEFAULT '';
ALTER TABLE companies ADD COLUMN iban        TEXT NOT NULL DEFAULT '';
ALTER TABLE companies ADD COLUMN fskatt      BOOLEAN NOT NULL DEFAULT false;
-- Key attribution: which master key (by non-secret fingerprint) wrapped this
-- company's DEK, and a version counter. This is the seam that makes future
-- master-key rotation possible (re-wrap DEKs whose key_id is the old fingerprint).
ALTER TABLE companies ADD COLUMN key_id      TEXT NOT NULL DEFAULT '';
ALTER TABLE companies ADD COLUMN key_version INT  NOT NULL DEFAULT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE companies DROP COLUMN IF EXISTS key_version;
ALTER TABLE companies DROP COLUMN IF EXISTS key_id;
ALTER TABLE companies DROP COLUMN IF EXISTS fskatt;
ALTER TABLE companies DROP COLUMN IF EXISTS iban;
ALTER TABLE companies DROP COLUMN IF EXISTS bankgiro;
ALTER TABLE companies DROP COLUMN IF EXISTS city;
ALTER TABLE companies DROP COLUMN IF EXISTS postal_code;
ALTER TABLE companies DROP COLUMN IF EXISTS address;
ALTER TABLE companies DROP COLUMN IF EXISTS momsregnr;
-- +goose StatementEnd
