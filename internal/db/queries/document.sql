-- name: InsertDocument :one
-- filename + note are stored encrypted (filename_enc/note_enc); the legacy
-- cleartext columns default to '' and are no longer written.
INSERT INTO documents (company_id, mime, byte_size, content_enc, sha256, filename_enc, note_enc)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: GetDocumentContent :one
SELECT company_id, filename_enc, mime, content_enc FROM documents WHERE id = $1;

-- name: ListDocumentMeta :many
-- Metadata only (never the blob): the receipt inbox.
SELECT id, filename_enc, mime, byte_size, note_enc, supplier_invoice_id, created_at
FROM documents WHERE company_id = $1 ORDER BY created_at DESC;

-- name: ListDocumentsForSupplier :many
SELECT id, filename_enc, mime, byte_size, note_enc, created_at
FROM documents WHERE company_id = $1 AND supplier_invoice_id = $2 ORDER BY created_at;

-- name: AttachDocumentToSupplier :execrows
UPDATE documents SET supplier_invoice_id = $3
WHERE id = $1 AND company_id = $2;

-- name: DeleteDocument :execrows
DELETE FROM documents WHERE id = $1 AND company_id = $2 AND supplier_invoice_id IS NULL AND verification_id IS NULL;

-- name: ListDocumentsForPIIBackfill :many
-- Rows written before filename/note were encrypted: cleartext still present and
-- the encrypted column empty. Returns the cleartext so a per-company DEK can
-- encrypt it, then SetDocumentPIIEnc blanks the cleartext.
SELECT id, company_id, filename, note FROM documents
WHERE filename_enc = '' AND (filename <> '' OR note <> '');

-- name: SetDocumentPIIEnc :exec
UPDATE documents SET filename_enc = $2, note_enc = $3, filename = '', note = '' WHERE id = $1;

-- name: AnonymizeOrphanDocuments :execrows
-- Blank the PII metadata (filename + note) of documents that are NOT
-- verifikationsunderlag (not attached to a supplier invoice or a verification)
-- and are older than the cutoff. Such an orphan receipt is not räkenskaps-
-- information, so its identifying filename/note need not be retained. Rows are
-- kept for referential integrity.
UPDATE documents SET filename_enc = '', note_enc = ''
WHERE company_id = $1 AND supplier_invoice_id IS NULL AND verification_id IS NULL
  AND (filename_enc <> '' OR note_enc <> '') AND created_at < $2;
