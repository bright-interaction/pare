// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"bytes"
	"context"
	"testing"

	"github.com/brightinteraction/pare/internal/bank"
	"github.com/brightinteraction/pare/internal/invoice"
	"github.com/brightinteraction/pare/internal/ledger"
	"github.com/brightinteraction/pare/internal/moms"
)

func TestBankReconciliation(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()
	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")
	cust, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "customer", Name: "Kund AB", OrgNr: "556100-2222"})
	invID, _ := s.CreateInvoice(ctx, co, cust, invoice.Invoice{Lines: []invoice.Line{
		{Description: "Tjänst", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(10000, 0), VATCode: moms.SE25},
	}})
	s.FinalizeInvoice(ctx, co, invID, day("2026-03-01"), day("2026-03-31")) // gross 12500

	entries := []bank.Entry{
		{Date: day("2026-03-05"), AmountOre: 1250000, Text: "Kund AB faktura"},   // matches the invoice
		{Date: day("2026-03-06"), AmountOre: -250000, Text: "Anthropic kortköp"}, // a cost
	}
	n, err := s.ImportBankStatement(ctx, co, "1930", entries)
	if err != nil || n != 2 {
		t.Fatalf("import: n=%d err=%v", n, err)
	}
	// Re-import is idempotent.
	if again, _ := s.ImportBankStatement(ctx, co, "1930", entries); again != 0 {
		t.Fatalf("re-import should add 0, added %d", again)
	}
	// Text is encrypted at rest.
	var enc string
	_ = pool.QueryRow(ctx, "SELECT text_enc FROM bank_transactions WHERE amount_ore=1250000").Scan(&enc)
	if bytes.Contains([]byte(enc), []byte("Kund")) {
		t.Fatalf("bank text stored in clear")
	}

	txns, _ := s.ListBankTransactions(ctx, co)
	if len(txns) != 2 {
		t.Fatalf("want 2 txns, got %d", len(txns))
	}
	var credit, debit BankTxnView
	for _, tx := range txns {
		if tx.IsCredit {
			credit = tx
		} else {
			debit = tx
		}
	}
	if credit.MatchNumber != "2026-0001" {
		t.Fatalf("credit not auto-matched: %q", credit.MatchNumber)
	}

	// Book the credit against the matched invoice -> invoice paid, books balance.
	if err := s.BookBankTxnToInvoice(ctx, co, credit.ID, credit.MatchID); err != nil {
		t.Fatalf("book invoice: %v", err)
	}
	inv, _ := s.InvoiceForRender(ctx, co, invID)
	if inv.Status != "paid" {
		t.Fatalf("invoice not settled: %s", inv.Status)
	}
	// Book the debit to a cost account.
	if err := s.BookBankTxnToAccount(ctx, co, debit.ID, "6540"); err != nil {
		t.Fatalf("book account: %v", err)
	}
	bal, _ := s.BalancesMap(ctx, co)
	if bal["6540"] != ledger.SEK(2500, 0) {
		t.Fatalf("cost not booked: 6540=%s", bal["6540"])
	}
	assertTrialZero(t, s, co)

	// Both are now booked; re-booking is refused.
	if err := s.BookBankTxnToAccount(ctx, co, debit.ID, "6540"); err != ErrTxnNotOpen {
		t.Fatalf("re-book should fail, got %v", err)
	}
}
