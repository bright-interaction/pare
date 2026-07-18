// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
package store

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/bright-interaction/pare/internal/bank"
	"github.com/bright-interaction/pare/internal/ledger"
	"github.com/bright-interaction/pare/internal/moms"
)

// TestBackfillDocumentPII is the migration dry-run for the filename/note
// encryption: a row written in the pre-encryption format (cleartext filename +
// note, empty *_enc) is encrypted in place, the cleartext is blanked, and reads
// still return the original values.
func TestBackfillDocumentPII(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()
	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")

	// Create a valid document (real content_enc), then rewrite it to the legacy
	// cleartext-PII shape an older binary produced.
	id, err := s.SaveDocument(ctx, co, "placeholder.pdf", "application/pdf", []byte("%PDF-1.4 body"), "")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	const oldName, oldNote = "kvitto_Anna_Andersson_19850101.pdf", "Privat anteckning om Anna"
	if _, err := pool.Exec(ctx,
		`UPDATE documents SET filename=$2, note=$3, filename_enc='', note_enc='' WHERE id=$1`,
		id, oldName, oldNote); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	n, err := s.BackfillDocumentPII(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 1 {
		t.Fatalf("backfill migrated %d, want 1", n)
	}

	// At rest: cleartext columns blanked, *_enc populated and not cleartext.
	var fnClear, noteClear, fnEnc, noteEnc string
	if err := pool.QueryRow(ctx,
		`SELECT filename, note, filename_enc, note_enc FROM documents WHERE id=$1`, id).
		Scan(&fnClear, &noteClear, &fnEnc, &noteEnc); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if fnClear != "" || noteClear != "" {
		t.Fatalf("cleartext not blanked: %q / %q", fnClear, noteClear)
	}
	if fnEnc == "" || fnEnc == oldName || bytes.Contains([]byte(fnEnc), []byte("Andersson")) {
		t.Fatalf("filename not encrypted at rest: %q", fnEnc)
	}

	// Reads round-trip to the originals.
	got, err := s.GetDocumentContent(ctx, co, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Filename != oldName {
		t.Fatalf("filename round-trip: got %q want %q", got.Filename, oldName)
	}
	metas, _ := s.ListDocuments(ctx, co)
	if len(metas) != 1 || metas[0].Filename != oldName || metas[0].Note != oldNote {
		t.Fatalf("list round-trip mismatch: %+v", metas)
	}

	// Idempotent: a second backfill migrates nothing.
	if n2, err := s.BackfillDocumentPII(ctx); err != nil || n2 != 0 {
		t.Fatalf("second backfill not a no-op: n=%d err=%v", n2, err)
	}
}

// TestAnonymizeNonRetainedPII locks that never-booked bank lines and orphan
// documents have their PII blanked, while booked lines and attached documents
// (räkenskapsinformation) are retained untouched.
func TestAnonymizeNonRetainedPII(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()
	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")

	// A never-booked (unmatched) bank line with PII.
	if _, err := s.ImportBankStatement(ctx, co, "1930", []bank.Entry{
		{Date: day("2026-01-02"), AmountOre: -4200, Text: "Sven Svensson privat"},
	}); err != nil {
		t.Fatalf("import bank: %v", err)
	}
	// An orphan (unattached) document with PII.
	orphan, err := s.SaveDocument(ctx, co, "orphan_Sven.pdf", "application/pdf", []byte("%PDF orphan"), "om Sven")
	if err != nil {
		t.Fatalf("save orphan: %v", err)
	}
	// A retained document: attach to a supplier invoice -> verifikationsunderlag.
	sup, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "supplier", Name: "Anthropic PBC", OrgNr: "US-0"})
	inv, _ := s.CreateSupplierInvoice(ctx, co, sup, "INV-9", day("2026-03-01"), day("2026-03-31"), "", ledger.SEK(1000, 0), moms.PIMP, "API")
	retainedDoc, _ := s.SaveDocument(ctx, co, "faktura_retained.pdf", "application/pdf", []byte("%PDF keep"), "keep me")
	if err := s.AttachDocumentToSupplier(ctx, co, retainedDoc, inv); err != nil {
		t.Fatalf("attach: %v", err)
	}

	bankN, docN, err := s.AnonymizeNonRetainedPII(ctx, co, time.Now())
	if err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	if bankN != 1 || docN != 1 {
		t.Fatalf("anonymized bank=%d docs=%d, want 1/1", bankN, docN)
	}

	// Non-retained PII is gone at rest.
	var bankText string
	_ = pool.QueryRow(ctx, `SELECT text_enc FROM bank_transactions WHERE company_id=$1`, co).Scan(&bankText)
	if bankText != "" {
		t.Fatalf("unmatched bank text not anonymized: %q", bankText)
	}
	var orphanFn string
	_ = pool.QueryRow(ctx, `SELECT filename_enc FROM documents WHERE id=$1`, orphan).Scan(&orphanFn)
	if orphanFn != "" {
		t.Fatalf("orphan document filename not anonymized: %q", orphanFn)
	}

	// Retained document keeps its (encrypted) filename.
	got, err := s.GetDocumentContent(ctx, co, retainedDoc)
	if err != nil {
		t.Fatalf("get retained: %v", err)
	}
	if got.Filename != "faktura_retained.pdf" {
		t.Fatalf("retained document PII was wrongly anonymized: %q", got.Filename)
	}

	// Idempotent: a second run changes nothing.
	if b2, d2, err := s.AnonymizeNonRetainedPII(ctx, co, time.Now()); err != nil || b2 != 0 || d2 != 0 {
		t.Fatalf("second anonymize not a no-op: bank=%d docs=%d err=%v", b2, d2, err)
	}
}
