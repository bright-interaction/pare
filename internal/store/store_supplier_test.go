// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"testing"

	"github.com/bright-interaction/pare/internal/ledger"
	"github.com/bright-interaction/pare/internal/moms"
)

// A foreign-service supplier bill (e.g. Anthropic) self-assesses forvarvsmoms on
// finalize, settles cleanly on payment, and leaves balanced books throughout.
func TestSupplierInvoiceForeignService(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")
	sup, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "supplier", Name: "Anthropic PBC", OrgNr: "US-000"})

	net := ledger.SEK(10000, 0)
	id, err := s.CreateSupplierInvoice(ctx, co, sup, "INV-42", day("2026-03-01"), day("2026-03-31"), "", net, moms.PIMP, "API mars")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.FinalizeSupplierInvoice(ctx, co, id); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	bal, _ := s.BalancesMap(ctx, co)
	if bal["4531"] != net {
		t.Fatalf("cost not booked to 4531: %s", bal["4531"])
	}
	if bal["2614"] != -ledger.SEK(2500, 0) { // fiktiv utgaende (credit)
		t.Fatalf("2614 = %s, want -2500", bal["2614"])
	}
	if bal["2645"] != ledger.SEK(2500, 0) { // berak. ingaende (debit)
		t.Fatalf("2645 = %s, want 2500", bal["2645"])
	}
	if bal["2440"] != -net { // owed to supplier
		t.Fatalf("2440 = %s, want -10000", bal["2440"])
	}
	assertTrialZero(t, s, co)

	// Reverse charge => no VAT cash owed, so payment equals the bare net.
	if _, err := s.RecordSupplierPayment(ctx, co, id, day("2026-03-20"), "1930", net); err != nil {
		t.Fatalf("pay: %v", err)
	}
	bal, _ = s.BalancesMap(ctx, co)
	if bal["2440"] != 0 {
		t.Fatalf("supplier debt not cleared: %s", bal["2440"])
	}
	if bal["1930"] != -net {
		t.Fatalf("bank not credited: %s", bal["1930"])
	}
	assertTrialZero(t, s, co)

	v, _ := s.SupplierInvoiceForView(ctx, co, id)
	if v.Status != "paid" {
		t.Fatalf("status = %s", v.Status)
	}
	if _, err := s.RecordSupplierPayment(ctx, co, id, day("2026-03-20"), "1930", net); err != ErrNotFinalized {
		t.Fatalf("re-pay should fail, got %v", err)
	}
}

// A domestic supplier bill deducts input VAT and is paid gross.
func TestSupplierInvoiceDomestic(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")
	sup, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "supplier", Name: "Svensk IT AB", OrgNr: "556999-0000"})

	net := ledger.SEK(8000, 0)
	id, _ := s.CreateSupplierInvoice(ctx, co, sup, "77", day("2026-04-01"), day("2026-04-30"), "6540", net, moms.PD25, "IT-tjänst")
	if _, err := s.FinalizeSupplierInvoice(ctx, co, id); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	bal, _ := s.BalancesMap(ctx, co)
	if bal["6540"] != net {
		t.Fatalf("cost = %s", bal["6540"])
	}
	if bal["2640"] != ledger.SEK(2000, 0) { // input VAT
		t.Fatalf("2640 = %s, want 2000", bal["2640"])
	}
	gross := ledger.SEK(10000, 0)
	if bal["2440"] != -gross {
		t.Fatalf("2440 = %s, want -10000", bal["2440"])
	}
	assertTrialZero(t, s, co)

	// Paying the net instead of the gross is rejected.
	if _, err := s.RecordSupplierPayment(ctx, co, id, day("2026-04-20"), "1930", net); err != ErrPaymentMismatch {
		t.Fatalf("want ErrPaymentMismatch, got %v", err)
	}
	if _, err := s.RecordSupplierPayment(ctx, co, id, day("2026-04-20"), "1930", gross); err != nil {
		t.Fatalf("pay gross: %v", err)
	}
	assertTrialZero(t, s, co)
}
