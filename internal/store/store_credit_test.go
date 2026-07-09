// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"testing"

	"github.com/brightinteraction/pare/internal/invoice"
	"github.com/brightinteraction/pare/internal/ledger"
	"github.com/brightinteraction/pare/internal/moms"
)

// Crediting a finalized invoice posts a reversing verifikat (books return to
// zero), marks the original cancelled, and produces a credit note that
// references it.
func TestCreditInvoice(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()
	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")
	cust, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "customer", Name: "Kund AB", OrgNr: "556100-2222"})
	invID, _ := s.CreateInvoice(ctx, co, cust, invoice.Invoice{Lines: []invoice.Line{
		{Description: "Tjänst", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(10000, 0), VATCode: moms.SE25},
	}})
	if _, _, err := s.FinalizeInvoice(ctx, co, invID, day("2026-02-01"), day("2026-03-03")); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	creditID, number, err := s.CreditInvoice(ctx, co, invID)
	if err != nil {
		t.Fatalf("credit: %v", err)
	}

	// The reversing credit note zeroes the ledger.
	assertTrialZero(t, s, co)
	bal, _ := s.BalancesMap(ctx, co)
	if bal["1510"] != 0 || bal["3001"] != 0 || bal["2611"] != 0 {
		t.Fatalf("books not reversed: 1510=%s 3001=%s 2611=%s", bal["1510"], bal["3001"], bal["2611"])
	}

	orig, _ := s.InvoiceForRender(ctx, co, invID)
	if orig.Status != "credited" {
		t.Fatalf("original not credited: %s", orig.Status)
	}
	credit, _ := s.InvoiceForRender(ctx, co, creditID)
	if !credit.IsCredit || credit.CreditsNumber != orig.Number {
		t.Fatalf("credit note not linked: isCredit=%v credits=%q orig=%q", credit.IsCredit, credit.CreditsNumber, orig.Number)
	}
	if credit.Number != number {
		t.Fatalf("returned number mismatch")
	}
	// A cancelled invoice cannot be credited again.
	if _, _, err := s.CreditInvoice(ctx, co, invID); err != ErrNotFinalized {
		t.Fatalf("re-credit should fail, got %v", err)
	}
}
