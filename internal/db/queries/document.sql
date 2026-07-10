-- name: InsertDocument :one
INSERT INTO documents (company_id, filename, mime, byte_size, content_enc, sha256, note)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: GetDocumentContent :one
SELECT company_id, filename, mime, content_enc FROM documents WHERE id = $1;

-- name: ListDocumentMeta :many
-- Metadata only (never the blob): the receipt inbox.
SELECT id, filename, mime, byte_size, note, supplier_invoice_id, created_at
FROM documents WHERE company_id = $1 ORDER BY created_at DESC;

-- name: ListDocumentsForSupplier :many
SELECT id, filename, mime, byte_size, note, created_at
FROM documents WHERE company_id = $1 AND supplier_invoice_id = $2 ORDER BY created_at;

-- name: AttachDocumentToSupplier :execrows
UPDATE documents SET supplier_invoice_id = $3
WHERE id = $1 AND company_id = $2;

-- name: DeleteDocument :execrows
DELETE FROM documents WHERE id = $1 AND company_id = $2 AND supplier_invoice_id IS NULL AND verification_id IS NULL;
