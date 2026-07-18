// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bright-interaction/pare/internal/crypto"
	gen "github.com/bright-interaction/pare/internal/db/generated"
)

// ErrDocumentAttached is returned when deleting a document still linked to a
// booked record (it is verifikationsunderlag and must be retained).
var ErrDocumentAttached = errors.New("store: document is attached to a booked record")

// DocumentMeta is a receipt/document without its (encrypted) content.
type DocumentMeta struct {
	ID        uuid.UUID
	Filename  string
	Mime      string
	ByteSize  int64
	Note      string
	Attached  bool
	CreatedAt time.Time
}

// DocumentFile is a decrypted document ready to serve.
type DocumentFile struct {
	Filename string
	Mime     string
	Content  []byte
}

// encField encrypts a field value with the DEK, mapping "" to "" so an empty
// note/filename is not stored as ciphertext (matches CreateCounterparty).
func encField(dek *crypto.DEK, v string) (string, error) {
	if v == "" {
		return "", nil
	}
	return dek.EncryptField([]byte(v))
}

// decField decrypts a field value, mapping "" to "". A decrypt error is
// swallowed (blank field) so one corrupt row never breaks a whole list, mirroring
// the bank text_enc read path.
func decField(dek *crypto.DEK, v string) string {
	if v == "" {
		return ""
	}
	if b, err := dek.DecryptField(v); err == nil {
		return string(b)
	}
	return ""
}

// SaveDocument encrypts a file with the company DEK and stores it. The plaintext
// content, filename and note are never persisted in cleartext and never leave Pare.
func (s *Store) SaveDocument(ctx context.Context, companyID uuid.UUID, filename, mime string, content []byte, note string) (uuid.UUID, error) {
	if len(content) == 0 {
		return uuid.Nil, errors.New("store: empty document")
	}
	dek, err := s.companyDEK(ctx, companyID)
	if err != nil {
		return uuid.Nil, err
	}
	enc, err := dek.EncryptBytes(content)
	if err != nil {
		return uuid.Nil, err
	}
	filenameEnc, err := encField(dek, filename)
	if err != nil {
		return uuid.Nil, err
	}
	noteEnc, err := encField(dek, note)
	if err != nil {
		return uuid.Nil, err
	}
	sum := sha256.Sum256(content)
	id, err := s.q.InsertDocument(ctx, gen.InsertDocumentParams{
		CompanyID: companyID, Mime: mime,
		ByteSize: int64(len(content)), ContentEnc: enc, Sha256: hex.EncodeToString(sum[:]),
		FilenameEnc: filenameEnc, NoteEnc: noteEnc,
	})
	if err != nil {
		return uuid.Nil, err
	}
	// Audit detail is the mime, not the filename: the filename is PII and the
	// entity_id (doc id) already gives the trail its target.
	if err := s.logAudit(ctx, s.q, companyID, "upload_document", "document", id.String(), mime); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// GetDocumentContent decrypts a document, enforcing company scope.
func (s *Store) GetDocumentContent(ctx context.Context, companyID, id uuid.UUID) (DocumentFile, error) {
	row, err := s.q.GetDocumentContent(ctx, id)
	if err != nil {
		return DocumentFile{}, err
	}
	if row.CompanyID != companyID {
		return DocumentFile{}, ErrForeignCompany
	}
	dek, err := s.companyDEK(ctx, companyID)
	if err != nil {
		return DocumentFile{}, err
	}
	plain, err := dek.DecryptBytes(row.ContentEnc)
	if err != nil {
		return DocumentFile{}, err
	}
	return DocumentFile{Filename: decField(dek, row.FilenameEnc), Mime: row.Mime, Content: plain}, nil
}

// ListDocuments returns document metadata (no blobs) for the receipt inbox.
func (s *Store) ListDocuments(ctx context.Context, companyID uuid.UUID) ([]DocumentMeta, error) {
	rows, err := s.q.ListDocumentMeta(ctx, companyID)
	if err != nil {
		return nil, err
	}
	dek, err := s.companyDEK(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]DocumentMeta, len(rows))
	for i, r := range rows {
		out[i] = DocumentMeta{ID: r.ID, Filename: decField(dek, r.FilenameEnc), Mime: r.Mime, ByteSize: r.ByteSize, Note: decField(dek, r.NoteEnc), Attached: r.SupplierInvoiceID.Valid, CreatedAt: r.CreatedAt.Time}
	}
	return out, nil
}

// ListDocumentsForSupplier returns documents attached to a supplier invoice.
func (s *Store) ListDocumentsForSupplier(ctx context.Context, companyID, supplierID uuid.UUID) ([]DocumentMeta, error) {
	rows, err := s.q.ListDocumentsForSupplier(ctx, gen.ListDocumentsForSupplierParams{CompanyID: companyID, SupplierInvoiceID: pgUUID(supplierID)})
	if err != nil {
		return nil, err
	}
	dek, err := s.companyDEK(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]DocumentMeta, len(rows))
	for i, r := range rows {
		out[i] = DocumentMeta{ID: r.ID, Filename: decField(dek, r.FilenameEnc), Mime: r.Mime, ByteSize: r.ByteSize, Note: decField(dek, r.NoteEnc), Attached: true, CreatedAt: r.CreatedAt.Time}
	}
	return out, nil
}

// AttachDocumentToSupplier links a document to a supplier invoice.
func (s *Store) AttachDocumentToSupplier(ctx context.Context, companyID, docID, supplierID uuid.UUID) error {
	n, err := s.q.AttachDocumentToSupplier(ctx, gen.AttachDocumentToSupplierParams{ID: docID, CompanyID: companyID, SupplierInvoiceID: pgUUID(supplierID)})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrForeignCompany
	}
	return s.logAudit(ctx, s.q, companyID, "attach_document", "supplier_invoice", supplierID.String(), docID.String())
}

// BackfillDocumentPII encrypts filename/note for documents written before those
// columns were encrypted, then blanks the cleartext (SetDocumentPIIEnc). Each
// company has its own DEK, so the migration is grouped per company. Idempotent:
// rows already migrated (filename_enc set) are not returned by the query. Returns
// the number of documents migrated. Run once at startup.
func (s *Store) BackfillDocumentPII(ctx context.Context) (int, error) {
	rows, err := s.q.ListDocumentsForPIIBackfill(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: list documents for PII backfill: %w", err)
	}
	deks := map[uuid.UUID]*crypto.DEK{}
	migrated := 0
	for _, r := range rows {
		dek, ok := deks[r.CompanyID]
		if !ok {
			dek, err = s.companyDEK(ctx, r.CompanyID)
			if err != nil {
				return migrated, fmt.Errorf("store: dek for company %s: %w", r.CompanyID, err)
			}
			deks[r.CompanyID] = dek
		}
		filenameEnc, err := encField(dek, r.Filename)
		if err != nil {
			return migrated, err
		}
		noteEnc, err := encField(dek, r.Note)
		if err != nil {
			return migrated, err
		}
		if err := s.q.SetDocumentPIIEnc(ctx, gen.SetDocumentPIIEncParams{
			ID: r.ID, FilenameEnc: filenameEnc, NoteEnc: noteEnc,
		}); err != nil {
			return migrated, fmt.Errorf("store: set document PII enc %s: %w", r.ID, err)
		}
		migrated++
	}
	return migrated, nil
}

// AnonymizeNonRetainedPII blanks the PII of records that are NOT räkenskaps-
// information for one company and are older than cutoff: never-booked bank lines
// (text_enc) and orphan documents (filename_enc/note_enc). Booked/attached
// records are retained untouched. Audited when anything changed. Returns the
// number of bank lines and documents anonymized.
func (s *Store) AnonymizeNonRetainedPII(ctx context.Context, companyID uuid.UUID, cutoff time.Time) (bankLines, docs int64, err error) {
	cut := pgtype.Timestamptz{Time: cutoff, Valid: true}
	bankLines, err = s.q.AnonymizeNonRetainedBankTxns(ctx, gen.AnonymizeNonRetainedBankTxnsParams{CompanyID: companyID, CreatedAt: cut})
	if err != nil {
		return 0, 0, fmt.Errorf("store: anonymize bank txns: %w", err)
	}
	docs, err = s.q.AnonymizeOrphanDocuments(ctx, gen.AnonymizeOrphanDocumentsParams{CompanyID: companyID, CreatedAt: cut})
	if err != nil {
		return bankLines, 0, fmt.Errorf("store: anonymize orphan documents: %w", err)
	}
	if bankLines > 0 || docs > 0 {
		if err := s.logAudit(ctx, s.q, companyID, "anonymize_non_retained_pii", "company", companyID.String(),
			fmt.Sprintf("bank_lines=%d documents=%d", bankLines, docs)); err != nil {
			return bankLines, docs, err
		}
	}
	return bankLines, docs, nil
}

// DeleteDocument removes an unattached document (an attached one is retained
// verifikationsunderlag).
func (s *Store) DeleteDocument(ctx context.Context, companyID, id uuid.UUID) error {
	n, err := s.q.DeleteDocument(ctx, gen.DeleteDocumentParams{ID: id, CompanyID: companyID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrDocumentAttached
	}
	return s.logAudit(ctx, s.q, companyID, "delete_document", "document", id.String(), "")
}
