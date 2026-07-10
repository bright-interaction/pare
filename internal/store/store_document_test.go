// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/brightinteraction/pare/internal/ledger"
	"github.com/brightinteraction/pare/internal/moms"
)

// A receipt is encrypted at rest, round-trips on read, and an attached one
// (verifikationsunderlag) cannot be deleted.
func TestDocumentEncryptedAndRetention(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()
	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")

	plain := []byte("%PDF-1.4 secret receipt from Anthropic PBC 100 USD")
	id, err := s.SaveDocument(ctx, co, "kvitto.pdf", "application/pdf", plain, "Anthropic mars")
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// Round-trips on read.
	got, err := s.GetDocumentContent(ctx, co, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got.Content, plain) || got.Filename != "kvitto.pdf" {
		t.Fatalf("round-trip mismatch")
	}

	// The stored blob must be ciphertext (no plaintext at rest).
	var enc []byte
	_ = pool.QueryRow(ctx, "SELECT content_enc FROM documents WHERE id=$1", id).Scan(&enc)
	if bytes.Contains(enc, []byte("Anthropic")) || bytes.Contains(enc, []byte("%PDF")) {
		t.Fatalf("document stored in clear at rest")
	}

	// List returns metadata, not the blob.
	metas, _ := s.ListDocuments(ctx, co)
	if len(metas) != 1 || metas[0].Attached {
		t.Fatalf("unexpected inbox: %+v", metas)
	}

	// Attach to a supplier invoice -> becomes underlag -> cannot be deleted.
	sup, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "supplier", Name: "Anthropic PBC", OrgNr: "US-0"})
	inv, _ := s.CreateSupplierInvoice(ctx, co, sup, "INV-1", day("2026-03-01"), day("2026-03-31"), "", ledger.SEK(1000, 0), moms.PIMP, "API")
	if err := s.AttachDocumentToSupplier(ctx, co, id, inv); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.DeleteDocument(ctx, co, id); err != ErrDocumentAttached {
		t.Fatalf("attached document should be retained, got %v", err)
	}

	// Cross-company read is refused.
	if _, err := s.GetDocumentContent(ctx, uuid.New(), id); err != ErrForeignCompany {
		t.Fatalf("want ErrForeignCompany, got %v", err)
	}
}
