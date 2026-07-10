// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/bright-interaction/pare/internal/invoice"
	"github.com/bright-interaction/pare/internal/ledger"
	"github.com/bright-interaction/pare/internal/moms"
)

// A domestic-SEK invoice settles cleanly: the bank account is debited, the
// receivable (1510) is cleared, no exchange difference, and the invoice is paid.
func TestRecordPaymentSEK(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, _ := s.BootstrapCompany(ctx, "Bright Interaction AB", "556000-0000")
	cust, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "customer", Name: "Kund AB", OrgNr: "556100-2222"})

	invID, _ := s.CreateInvoice(ctx, co, cust, invoice.Invoice{Lines: []invoice.Line{
		{Description: "Konsultarvode", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(10000, 0), VATCode: moms.SE25},
	}})
	if _, _, err := s.FinalizeInvoice(ctx, co, invID, day("2026-02-01"), day("2026-03-03")); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	view, _ := s.InvoiceForRender(ctx, co, invID)
	gross := view.TotalSEK // 10000 + 25% = 12500 SEK

	if _, err := s.RecordPayment(ctx, co, invID, day("2026-03-01"), "1930", gross); err != nil {
		t.Fatalf("record payment: %v", err)
	}

	bal, _ := s.BalancesMap(ctx, co)
	if bal["1510"] != 0 {
		t.Fatalf("receivable not cleared: 1510=%s", bal["1510"])
	}
	if bal["1930"] != gross {
		t.Fatalf("bank not debited by gross: 1930=%s want %s", bal["1930"], gross)
	}
	if bal["3960"] != 0 || bal["7960"] != 0 {
		t.Fatalf("unexpected FX diff on a SEK invoice: 3960=%s 7960=%s", bal["3960"], bal["7960"])
	}
	assertTrialZero(t, s, co)

	paid, _ := s.InvoiceForRender(ctx, co, invID)
	if paid.Status != "paid" {
		t.Fatalf("status not paid: %s", paid.Status)
	}
	// A paid invoice cannot be paid again.
	if _, err := s.RecordPayment(ctx, co, invID, day("2026-03-01"), "1930", gross); err != ErrNotFinalized {
		t.Fatalf("want ErrNotFinalized on re-pay, got %v", err)
	}
}

// A SEK invoice can be paid in instalments; a final payment within an öre
// tolerance settles it via 3740; overpaying beyond the tolerance is rejected.
func TestRecordPaymentPartialAndRounding(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, _ := s.BootstrapCompany(ctx, "Bright Interaction AB", "556000-0000")
	cust, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "customer", Name: "Kund AB", OrgNr: "556100-2222"})
	invID, _ := s.CreateInvoice(ctx, co, cust, invoice.Invoice{Lines: []invoice.Line{
		{Description: "Tjänst", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(10000, 0), VATCode: moms.SE25},
	}})
	if _, _, err := s.FinalizeInvoice(ctx, co, invID, day("2026-02-01"), day("2026-03-03")); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	gross := ledger.SEK(12500, 0) // 10000 + 25%

	// Partial payment of 5000: invoice stays finalized, receivable partly open.
	if _, err := s.RecordPayment(ctx, co, invID, day("2026-03-01"), "1930", ledger.SEK(5000, 0)); err != nil {
		t.Fatalf("partial: %v", err)
	}
	v, _ := s.InvoiceForRender(ctx, co, invID)
	if v.Status != "finalized" || v.Outstanding() != gross-ledger.SEK(5000, 0) {
		t.Fatalf("after partial: status=%s outstanding=%s", v.Status, v.Outstanding())
	}
	bal, _ := s.BalancesMap(ctx, co)
	if bal["1510"] != gross-ledger.SEK(5000, 0) {
		t.Fatalf("receivable after partial = %s", bal["1510"])
	}

	// Final payment 50 öre short of the balance: settles via 3740 öresavrundning.
	rest := v.Outstanding() - ledger.Amount(50)
	if _, err := s.RecordPayment(ctx, co, invID, day("2026-03-10"), "1930", rest); err != nil {
		t.Fatalf("final: %v", err)
	}
	paid, _ := s.InvoiceForRender(ctx, co, invID)
	if paid.Status != "paid" {
		t.Fatalf("not settled: %s", paid.Status)
	}
	bal, _ = s.BalancesMap(ctx, co)
	if bal["1510"] != 0 {
		t.Fatalf("receivable not cleared: 1510=%s", bal["1510"])
	}
	if bal["3740"] != ledger.Amount(50) { // 50 öre written off (debit)
		t.Fatalf("öresavrundning not booked to 3740: %s", bal["3740"])
	}
	if bal["3960"] != 0 || bal["7960"] != 0 {
		t.Fatalf("FX accounts touched on a SEK invoice: 3960=%s 7960=%s", bal["3960"], bal["7960"])
	}
	assertTrialZero(t, s, co)
}

// Overpaying a SEK invoice beyond the öre tolerance is a refund, not a payment.
func TestRecordPaymentOverpayRejected(t *testing.T) {
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
	if _, err := s.RecordPayment(ctx, co, invID, day("2026-03-01"), "1930", ledger.SEK(13000, 0)); err != ErrPaymentMismatch {
		t.Fatalf("want ErrPaymentMismatch on overpay, got %v", err)
	}
}

// A foreign-currency invoice booked at one rate but received at a stronger rate
// posts the surplus as a currency gain (3960); the books still balance.
func TestRecordPaymentFXGain(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, _ := s.BootstrapCompany(ctx, "Bright Interaction AB", "556000-0000")
	cust, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "customer", Name: "EU Kund GmbH", OrgNr: "DE123456789"})

	// 1000 EUR net, 25% VAT -> 1250 EUR, booked at 11.00 SEK/EUR = 13 750 SEK.
	invID, _ := s.CreateInvoice(ctx, co, cust, invoice.Invoice{
		Currency: "EUR", RatePPM: 11_000_000,
		Lines: []invoice.Line{
			{Description: "Retainer", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(1000, 0), VATCode: moms.SE25},
		},
	})
	if _, _, err := s.FinalizeInvoice(ctx, co, invID, day("2026-02-01"), day("2026-03-03")); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	view, _ := s.InvoiceForRender(ctx, co, invID)
	gross := view.TotalSEK
	if gross != ledger.SEK(13750, 0) {
		t.Fatalf("unexpected booked gross: %s", gross)
	}

	received := ledger.SEK(14000, 0) // rate moved: 250 SEK more than booked
	if _, err := s.RecordPayment(ctx, co, invID, day("2026-03-01"), "1930", received); err != nil {
		t.Fatalf("record payment: %v", err)
	}

	bal, _ := s.BalancesMap(ctx, co)
	if bal["1510"] != 0 {
		t.Fatalf("receivable not cleared: 1510=%s", bal["1510"])
	}
	if bal["1930"] != received {
		t.Fatalf("bank not debited by received: 1930=%s", bal["1930"])
	}
	if bal["3960"] != -ledger.SEK(250, 0) { // income (credit) shows as negative net
		t.Fatalf("FX gain not booked to 3960: %s", bal["3960"])
	}
	if bal["7960"] != 0 {
		t.Fatalf("unexpected loss on a gain: 7960=%s", bal["7960"])
	}
	assertTrialZero(t, s, co)
}

// Received less than booked (weaker rate) posts a currency loss to 7960.
func TestRecordPaymentFXLoss(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, _ := s.BootstrapCompany(ctx, "Bright Interaction AB", "556000-0000")
	cust, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "customer", Name: "EU Kund GmbH", OrgNr: "DE123456789"})

	invID, _ := s.CreateInvoice(ctx, co, cust, invoice.Invoice{
		Currency: "EUR", RatePPM: 11_000_000,
		Lines: []invoice.Line{
			{Description: "Retainer", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(1000, 0), VATCode: moms.SE25},
		},
	})
	if _, _, err := s.FinalizeInvoice(ctx, co, invID, day("2026-02-01"), day("2026-03-03")); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	received := ledger.SEK(13500, 0) // 250 SEK less than the 13 750 booked
	if _, err := s.RecordPayment(ctx, co, invID, day("2026-03-01"), "1930", received); err != nil {
		t.Fatalf("record payment: %v", err)
	}

	bal, _ := s.BalancesMap(ctx, co)
	if bal["1510"] != 0 {
		t.Fatalf("receivable not cleared: 1510=%s", bal["1510"])
	}
	if bal["7960"] != ledger.SEK(250, 0) { // expense (debit) shows as positive net
		t.Fatalf("FX loss not booked to 7960: %s", bal["7960"])
	}
	if bal["3960"] != 0 {
		t.Fatalf("unexpected gain on a loss: 3960=%s", bal["3960"])
	}
	assertTrialZero(t, s, co)
}

func assertTrialZero(t *testing.T, s *Store, co uuid.UUID) {
	t.Helper()
	tb, err := s.TrialBalance(context.Background(), co)
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	var total ledger.Amount
	for _, r := range tb {
		total += r.Net
	}
	if total != 0 {
		t.Fatalf("trial balance not zero: %s", total)
	}
}
