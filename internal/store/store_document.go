// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"

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

// SaveDocument encrypts a file with the company DEK and stores it. The plaintext
// is never persisted and never leaves Pare.
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
	sum := sha256.Sum256(content)
	id, err := s.q.InsertDocument(ctx, gen.InsertDocumentParams{
		CompanyID: companyID, Filename: filename, Mime: mime,
		ByteSize: int64(len(content)), ContentEnc: enc, Sha256: hex.EncodeToString(sum[:]), Note: note,
	})
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.logAudit(ctx, s.q, companyID, "upload_document", "document", id.String(), filename); err != nil {
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
	return DocumentFile{Filename: row.Filename, Mime: row.Mime, Content: plain}, nil
}

// ListDocuments returns document metadata (no blobs) for the receipt inbox.
func (s *Store) ListDocuments(ctx context.Context, companyID uuid.UUID) ([]DocumentMeta, error) {
	rows, err := s.q.ListDocumentMeta(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]DocumentMeta, len(rows))
	for i, r := range rows {
		out[i] = DocumentMeta{ID: r.ID, Filename: r.Filename, Mime: r.Mime, ByteSize: r.ByteSize, Note: r.Note, Attached: r.SupplierInvoiceID.Valid, CreatedAt: r.CreatedAt.Time}
	}
	return out, nil
}

// ListDocumentsForSupplier returns documents attached to a supplier invoice.
func (s *Store) ListDocumentsForSupplier(ctx context.Context, companyID, supplierID uuid.UUID) ([]DocumentMeta, error) {
	rows, err := s.q.ListDocumentsForSupplier(ctx, gen.ListDocumentsForSupplierParams{CompanyID: companyID, SupplierInvoiceID: pgUUID(supplierID)})
	if err != nil {
		return nil, err
	}
	out := make([]DocumentMeta, len(rows))
	for i, r := range rows {
		out[i] = DocumentMeta{ID: r.ID, Filename: r.Filename, Mime: r.Mime, ByteSize: r.ByteSize, Note: r.Note, Attached: true, CreatedAt: r.CreatedAt.Time}
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
