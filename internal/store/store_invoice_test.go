// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/brightinteraction/pare/internal/invoice"
	"github.com/brightinteraction/pare/internal/ledger"
	"github.com/brightinteraction/pare/internal/moms"
	"github.com/brightinteraction/pare/internal/render"
	"github.com/brightinteraction/pare/internal/sie"
)

func TestInvoiceFinalizeAndSIE(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, err := s.BootstrapCompany(ctx, "Bright Interaction AB", "556000-0000")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	cust, err := s.CreateCounterparty(ctx, co, Counterparty{Kind: "customer", Name: "Advokatbyrån Nord AB", OrgNr: "556677-8899"})
	if err != nil {
		t.Fatalf("counterparty: %v", err)
	}

	inv := invoice.Invoice{Lines: []invoice.Line{
		{Description: "Konsultarvode", QuantityMilli: 7500, UnitPriceOre: ledger.SEK(1200, 0), VATCode: moms.SE25},
		{Description: "Licens", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(2000, 0), VATCode: moms.SE25},
	}}
	invID, err := s.CreateInvoice(ctx, co, cust, inv)
	if err != nil {
		t.Fatalf("create invoice: %v", err)
	}

	verID, number, err := s.FinalizeInvoice(ctx, co, invID, day("2026-02-01"), day("2026-03-03"))
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if verID.String() == "" {
		t.Fatal("no verification id")
	}
	if number != "2026-0001" {
		t.Fatalf("first invoice number = %q, want 2026-0001", number)
	}

	// books still balance after auto-posting the invoice verifikat
	tb, err := s.TrialBalance(ctx, co)
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	var total ledger.Amount
	for _, r := range tb {
		total += r.Net
	}
	if total != 0 {
		t.Fatalf("trial balance not zero after finalize: %s", total)
	}

	// re-finalizing a non-draft is refused
	if _, _, err := s.FinalizeInvoice(ctx, co, invID, day("2026-02-01"), day("2026-03-03")); err != ErrNotDraft {
		t.Fatalf("want ErrNotDraft, got %v", err)
	}

	// SIE export from the DB round-trips and balances
	exp, err := s.ExportSIE(ctx, co, day("2026-07-09"))
	if err != nil {
		t.Fatalf("export sie: %v", err)
	}
	var buf bytes.Buffer
	if err := exp.Write(&buf); err != nil {
		t.Fatalf("write sie: %v", err)
	}
	back, err := sie.Parse(&buf)
	if err != nil {
		t.Fatalf("parse sie: %v", err)
	}
	if err := back.Balances(); err != nil {
		t.Fatalf("exported SIE unbalanced: %v", err)
	}
	if len(back.Vouchers) != 1 || back.Vouchers[0].Series != "F" {
		t.Fatalf("expected one F voucher, got %+v", back.Vouchers)
	}
	if back.CompanyName != "Bright Interaction AB" || back.OrgNr != "556000-0000" {
		t.Fatalf("company header wrong: %q %q", back.CompanyName, back.OrgNr)
	}
}

// Invoice numbers are gap-free and per fiscal year: sequential within a year and
// reset to 0001 at the year boundary (the old full-table count kept climbing).
func TestInvoiceNumberingPerYear(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()
	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")
	cust, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "customer", Name: "Kund AB", OrgNr: "556100-2222"})

	mk := func(d string) string {
		id, _ := s.CreateInvoice(ctx, co, cust, invoice.Invoice{Lines: []invoice.Line{
			{Description: "x", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(100, 0), VATCode: moms.SE25},
		}})
		_, num, err := s.FinalizeInvoice(ctx, co, id, day(d), day(d))
		if err != nil {
			t.Fatalf("finalize %s: %v", d, err)
		}
		return num
	}
	if got := mk("2026-03-01"); got != "2026-0001" {
		t.Fatalf("first 2026 = %q", got)
	}
	if got := mk("2026-06-01"); got != "2026-0002" {
		t.Fatalf("second 2026 = %q", got)
	}
	if got := mk("2027-01-10"); got != "2027-0001" {
		t.Fatalf("first 2027 should reset to 0001, got %q", got)
	}
	if got := mk("2027-02-10"); got != "2027-0002" {
		t.Fatalf("second 2027 = %q", got)
	}
}

func TestInvoicePDF(t *testing.T) {
	url := os.Getenv("PARE_TEST_GOTENBERG_URL")
	if url == "" {
		t.Skip("PARE_TEST_GOTENBERG_URL not set; skipping PDF render test")
	}
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, _ := s.BootstrapCompany(ctx, "Bright Interaction AB", "556000-0000")
	cust, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "customer", Name: "Kund AB", OrgNr: "556100-2222"})
	invID, _ := s.CreateInvoice(ctx, co, cust, invoice.Invoice{Lines: []invoice.Line{
		{Description: "Konsultarvode", QuantityMilli: 5000, UnitPriceOre: ledger.SEK(1500, 0), VATCode: moms.SE25},
	}})
	if _, _, err := s.FinalizeInvoice(ctx, co, invID, day("2026-02-01"), day("2026-03-03")); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	g := render.NewGotenberg(url)
	pdf, err := s.RenderInvoicePDF(ctx, g, co, invID)
	if err != nil {
		t.Fatalf("render pdf: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("not a PDF (len %d)", len(pdf))
	}
}
