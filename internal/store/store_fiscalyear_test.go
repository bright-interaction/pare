// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"testing"

	"github.com/bright-interaction/pare/internal/ledger"
)

// Closing a fiscal year posts the result to 2099, locks the period, and keeps the
// resultaträkning showing the real P&L while the balansräkning still balances
// (no double-counting of the result).
func TestCloseFiscalYear(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()
	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")
	if err := s.EnsureFiscalYear(ctx, co, 2026); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	var fy FiscalYear
	fys, _ := s.ListFiscalYears(ctx, co)
	for _, f := range fys {
		if f.Label == "2026" {
			fy = f
		}
	}
	if fy.ID == (fy.ID) && fy.Label != "2026" {
		t.Fatal("2026 fiscal year not seeded")
	}

	// Income 10000 + moms, expense 2000 -> result 8000 profit.
	s.PostVerification(ctx, co, "A", day("2026-03-01"), "sale", []ledger.Line{
		{Account: "1930", Debit: ledger.SEK(12500, 0)}, {Account: "3001", Credit: ledger.SEK(10000, 0)}, {Account: "2611", Credit: ledger.SEK(2500, 0)},
	}, [16]byte{})
	s.PostVerification(ctx, co, "A", day("2026-04-01"), "rent", []ledger.Line{
		{Account: "5010", Debit: ledger.SEK(2000, 0)}, {Account: "1930", Credit: ledger.SEK(2000, 0)},
	}, [16]byte{})

	if _, err := s.CloseFiscalYear(ctx, co, fy.ID); err != nil {
		t.Fatalf("close: %v", err)
	}

	bal, _ := s.BalancesMap(ctx, co)
	if bal["2099"] != -ledger.SEK(8000, 0) { // profit credited to equity
		t.Fatalf("2099 = %s, want -8000", bal["2099"])
	}
	if bal["3001"] != 0 || bal["5010"] != 0 { // P&L zeroed by the close
		t.Fatalf("P&L not zeroed: 3001=%s 5010=%s", bal["3001"], bal["5010"])
	}
	assertTrialZero(t, s, co)

	// Reports: resultaträkning (excl close series) still shows the real result;
	// balansräkning balances with the result on 2099 and no synthetic row.
	plTB, _ := s.TrialBalanceBetweenExclSeries(ctx, co, day("2026-01-01"), day("2026-12-31"), "O")
	asOfTB, _ := s.TrialBalanceAsOf(ctx, co, day("2026-12-31"))
	st := ledger.BuildStatementsPeriod(plTB, asOfTB, nil)
	if st.Result != ledger.SEK(8000, 0) {
		t.Fatalf("resultaträkning result = %s, want 8000 after close", st.Result)
	}
	if st.AssetTotal != st.EquityTotal+st.LiabilityTotal {
		t.Fatalf("balansräkning does not balance: %s != %s + %s", st.AssetTotal, st.EquityTotal, st.LiabilityTotal)
	}
	if st.EquityTotal != ledger.SEK(8000, 0) {
		t.Fatalf("equity = %s, want 8000 (2099, no double-count)", st.EquityTotal)
	}

	// The period is now locked and the year cannot be closed twice.
	if _, err := s.PostVerification(ctx, co, "A", day("2026-06-01"), "late", []ledger.Line{
		{Account: "1930", Debit: ledger.SEK(1, 0)}, {Account: "3001", Credit: ledger.SEK(1, 0)},
	}, [16]byte{}); err != ErrPeriodClosed {
		t.Fatalf("posting into a closed period should fail, got %v", err)
	}
	if _, err := s.CloseFiscalYear(ctx, co, fy.ID); err != ErrYearClosed {
		t.Fatalf("re-close should fail, got %v", err)
	}
}
