// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
package ledger

import "testing"

// A realistic set of balanced verifikat should yield a resultaträkning whose
// result matches Result(), and a balansräkning where assets equal equity plus
// liabilities (the fundamental accounting identity).
func TestBuildStatementsBalances(t *testing.T) {
	vs := []Verification{
		{Series: "F", Number: 1, Date: day("2026-01-15"), Description: "Faktura", Lines: []Line{
			{Account: "1510", Debit: SEK(12500, 0)},  // kundfordran
			{Account: "3001", Credit: SEK(10000, 0)}, // försäljning
			{Account: "2611", Credit: SEK(2500, 0)},  // utgående moms
		}},
		{Series: "B", Number: 2, Date: day("2026-02-01"), Description: "Betalning", Lines: []Line{
			{Account: "1930", Debit: SEK(12500, 0)},  // bank in
			{Account: "1510", Credit: SEK(12500, 0)}, // kundfordran cleared
		}},
		{Series: "A", Number: 3, Date: day("2026-02-05"), Description: "Hyra", Lines: []Line{
			{Account: "5010", Debit: SEK(2000, 0)},  // lokalhyra
			{Account: "2640", Debit: SEK(500, 0)},   // ingående moms
			{Account: "1930", Credit: SEK(2500, 0)}, // bank out
		}},
		{Series: "A", Number: 4, Date: day("2026-02-28"), Description: "Bankränta", Lines: []Line{
			{Account: "1930", Debit: SEK(30, 0)},  // ränta in
			{Account: "8310", Credit: SEK(30, 0)}, // ränteintäkt
		}},
	}
	tb := TrialBalance(vs)
	s := BuildStatements(tb, func(a string) string { return "" })

	// resultaträkning: income 10000, expense 2000, financial +30 -> result 8030
	if s.IncomeTotal != SEK(10000, 0) {
		t.Fatalf("income total = %s, want 10000", s.IncomeTotal)
	}
	if s.ExpenseTotal != SEK(2000, 0) {
		t.Fatalf("expense total = %s, want 2000", s.ExpenseTotal)
	}
	if s.FinancialTotal != SEK(30, 0) {
		t.Fatalf("financial total = %s, want 30", s.FinancialTotal)
	}
	if s.Result != SEK(8030, 0) {
		t.Fatalf("result = %s, want 8030", s.Result)
	}
	if s.Result != Result(vs) {
		t.Fatalf("statement result %s != Result() %s", s.Result, Result(vs))
	}

	// balansräkning identity: assets = equity + liabilities (with the synthetic
	// result folded into equity).
	if s.AssetTotal != s.EquityTotal+s.LiabilityTotal {
		t.Fatalf("balance sheet does not balance: assets %s != equity %s + liab %s",
			s.AssetTotal, s.EquityTotal, s.LiabilityTotal)
	}
	// assets: bank 12500-2500+30 = 10030, kundfordran 0
	if s.AssetTotal != SEK(10030, 0) {
		t.Fatalf("asset total = %s, want 10030", s.AssetTotal)
	}
	// liabilities: utgående moms 2500 - ingående moms 500 = 2000
	if s.LiabilityTotal != SEK(2000, 0) {
		t.Fatalf("liability total = %s, want 2000", s.LiabilityTotal)
	}
	// equity is only the synthetic result here (no posted equity): 8030
	if s.EquityTotal != SEK(8030, 0) {
		t.Fatalf("equity total = %s, want 8030", s.EquityTotal)
	}
}

// A loss (expenses exceed income) still balances, with a negative result folded
// into equity.
func TestBuildStatementsLoss(t *testing.T) {
	vs := []Verification{
		{Series: "A", Number: 1, Date: day("2026-01-10"), Description: "Konsult", Lines: []Line{
			{Account: "6550", Debit: SEK(5000, 0)},  // konsultarvode (cost)
			{Account: "1930", Credit: SEK(5000, 0)}, // bank out
		}},
	}
	s := BuildStatements(TrialBalance(vs), nil)
	if s.Result != SEK(-5000, 0) {
		t.Fatalf("result = %s, want -5000", s.Result)
	}
	if s.AssetTotal != s.EquityTotal+s.LiabilityTotal {
		t.Fatalf("loss balance sheet does not balance: %s != %s + %s",
			s.AssetTotal, s.EquityTotal, s.LiabilityTotal)
	}
}
