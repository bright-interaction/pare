-- +goose Up
-- +goose StatementBegin
-- documents.content_enc (the receipt bytes) was encrypted from day one, but the
-- filename and note columns were left in cleartext. Both are PII: an uploaded
-- receipt filename routinely carries a person's name / org-nr, and the note is
-- free text. Add encrypted columns matching the pare convention (bank_transactions
-- text_enc, counterparty *_enc). The cleartext columns are backfilled into the
-- _enc columns and blanked at startup (BackfillDocumentPII); a later migration
-- drops them once every deploy is confirmed on the new columns. filename gets a
-- DEFAULT so inserts can stop writing it.
ALTER TABLE documents ADD COLUMN filename_enc TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN note_enc TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ALTER COLUMN filename SET DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE documents ALTER COLUMN filename DROP DEFAULT;
ALTER TABLE documents DROP COLUMN note_enc;
ALTER TABLE documents DROP COLUMN filename_enc;
-- +goose StatementEnd
