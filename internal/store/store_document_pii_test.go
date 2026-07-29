// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
package store

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bright-interaction/pare/internal/bank"
	gen "github.com/bright-interaction/pare/internal/db/generated"
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

	n, skipped, err := s.BackfillDocumentPII(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 1 || skipped != 0 {
		t.Fatalf("backfill migrated %d skipped %d, want 1/0", n, skipped)
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
	if n2, sk2, err := s.BackfillDocumentPII(ctx); err != nil || n2 != 0 || sk2 != 0 {
		t.Fatalf("second backfill not a no-op: n=%d skipped=%d err=%v", n2, sk2, err)
	}
}

// TestBackfillDocumentPIIDoesNotGateBootOnUnusableDEK is the end-to-end repro
// for audit 2026-07-28 HIGH-2 against a real database: a company whose
// dek_wrapped no longer unwraps (rotated master key, corrupt byte) must not make
// the startup backfill return an error, because cmd/server/main.go turns that
// into os.Exit(1) before the listener is built and the selection query re-serves
// the same rows on every restart, so the server crash-loops forever.
//
// It mutates rows only. It never changes the schema of the shared test database.
func TestBackfillDocumentPIIDoesNotGateBootOnUnusableDEK(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()
	co, _ := s.BootstrapCompany(ctx, "Broken AB", "556000-0000")

	id, err := s.SaveDocument(ctx, co, "placeholder.pdf", "application/pdf", []byte("%PDF-1.4 body"), "")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	const oldName = "kvitto_Anna_Andersson_19850101.pdf"
	if _, err := pool.Exec(ctx,
		`UPDATE documents SET filename=$2, note='n', filename_enc='', note_enc='' WHERE id=$1`,
		id, oldName); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE companies SET dek_wrapped='not-a-valid-wrapped-dek' WHERE id=$1`, co); err != nil {
		t.Fatalf("corrupt dek: %v", err)
	}

	migrated, skipped, err := s.BackfillDocumentPII(ctx)
	if err != nil {
		t.Fatalf("boot gated on an unusable company DEK: %v", err)
	}
	if migrated != 0 || skipped != 1 {
		t.Fatalf("migrated=%d skipped=%d, want 0/1", migrated, skipped)
	}

	// The skipped row is left exactly as it was: no half-write that would make a
	// later repair lose the cleartext it could not encrypt yet.
	var fnClear, fnEnc string
	if err := pool.QueryRow(ctx, `SELECT filename, filename_enc FROM documents WHERE id=$1`, id).
		Scan(&fnClear, &fnEnc); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if fnClear != oldName || fnEnc != "" {
		t.Fatalf("skipped row was mutated: filename=%q filename_enc=%q", fnClear, fnEnc)
	}

	// Restarting reports the same thing rather than drifting or crashing.
	if m2, s2, err := s.BackfillDocumentPII(ctx); err != nil || m2 != 0 || s2 != 1 {
		t.Fatalf("restart path: migrated=%d skipped=%d err=%v, want 0/1/nil", m2, s2, err)
	}
}

// TestAnonymizeOrphanDocumentsHealsLegacyCleartext is the other half of HIGH-2.
// Because the backfill now skips a broken-DEK company instead of aborting boot,
// the retention sweep is the only thing that can ever erase that company's
// non-retained PII, and it must fire on the legacy cleartext columns (it has no
// DEK to look at filename_enc with).
func TestAnonymizeOrphanDocumentsHealsLegacyCleartext(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()
	co, _ := s.BootstrapCompany(ctx, "Broken AB", "556000-0000")

	orphan, err := s.SaveDocument(ctx, co, "placeholder.pdf", "application/pdf", []byte("%PDF orphan"), "")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE documents SET filename='kvitto_Sven_Svensson.pdf', note='om Sven', filename_enc='', note_enc='' WHERE id=$1`,
		orphan); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	docs, err := s.q.AnonymizeOrphanDocuments(ctx, gen.AnonymizeOrphanDocumentsParams{
		CompanyID: co, CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	if docs != 1 {
		t.Fatalf("anonymized %d orphan documents, want 1", docs)
	}
	var fn, note string
	if err := pool.QueryRow(ctx, `SELECT filename, note FROM documents WHERE id=$1`, orphan).Scan(&fn, &note); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if fn != "" || note != "" {
		t.Fatalf("legacy cleartext survived the sweep: filename=%q note=%q", fn, note)
	}
}

// TestAnonymizeOrphanDocumentsKeepsRetainedCleartext guards the widened
// predicate against over-reach on BOTH retention legs. A document attached to a
// supplier invoice and a document attached to a verification are
// verifikationsunderlag: Bokforingslagen requires the metadata retained, so the
// sweep must not blank their cleartext even though it now matches on it.
func TestAnonymizeOrphanDocumentsKeepsRetainedCleartext(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()
	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")

	sup, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "supplier", Name: "Anthropic PBC", OrgNr: "US-0"})
	inv, _ := s.CreateSupplierInvoice(ctx, co, sup, "INV-9", day("2026-03-01"), day("2026-03-31"), "", ledger.SEK(1000, 0), moms.PIMP, "API")
	ver, err := s.PostVerification(ctx, co, "A", day("2026-03-02"), "kontant", []ledger.Line{
		{Account: "1930", Credit: ledger.SEK(100, 0)},
		{Account: "5410", Debit: ledger.SEK(100, 0)},
	}, uuid.Nil)
	if err != nil {
		t.Fatalf("post verification: %v", err)
	}

	// Two legacy cleartext documents, one on each retention leg.
	onInvoice, _ := s.SaveDocument(ctx, co, "p1.pdf", "application/pdf", []byte("%PDF a"), "")
	onVerification, _ := s.SaveDocument(ctx, co, "p2.pdf", "application/pdf", []byte("%PDF b"), "")
	if _, err := pool.Exec(ctx,
		`UPDATE documents SET filename='underlag_Anna.pdf', note='underlag', filename_enc='', note_enc='',
		 supplier_invoice_id=$2 WHERE id=$1`, onInvoice, inv); err != nil {
		t.Fatalf("seed invoice underlag: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE documents SET filename='underlag_Bo.pdf', note='underlag', filename_enc='', note_enc='',
		 verification_id=$2 WHERE id=$1`, onVerification, ver); err != nil {
		t.Fatalf("seed verification underlag: %v", err)
	}

	docs, err := s.q.AnonymizeOrphanDocuments(ctx, gen.AnonymizeOrphanDocumentsParams{
		CompanyID: co, CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	if docs != 0 {
		t.Fatalf("sweep touched %d retained documents, want 0", docs)
	}
	for _, tc := range []struct {
		leg  string
		id   uuid.UUID
		want string
	}{
		{"supplier_invoice_id", onInvoice, "underlag_Anna.pdf"},
		{"verification_id", onVerification, "underlag_Bo.pdf"},
	} {
		var fn string
		if err := pool.QueryRow(ctx, `SELECT filename FROM documents WHERE id=$1`, tc.id).Scan(&fn); err != nil {
			t.Fatalf("read %s row: %v", tc.leg, err)
		}
		if fn != tc.want {
			t.Fatalf("%s leg: retained underlag was erased, filename=%q want %q", tc.leg, fn, tc.want)
		}
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
