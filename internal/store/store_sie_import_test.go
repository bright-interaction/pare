// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"strings"
	"testing"

	"github.com/bright-interaction/pare/internal/ledger"
	"github.com/bright-interaction/pare/internal/sie"
)

const sampleSIE = `#FLAGGA 0
#SIETYP 4
#ORGNR 556000-0000
#FNAMN "BI AB"
#RAR 0 20260101 20261231
#KONTO 1930 "Bank"
#KONTO 2081 "Aktiekapital"
#KONTO 3001 "Forsaljning"
#KONTO 2611 "Utg moms"
#IB 0 1930 10000.00
#IB 0 2081 -10000.00
#VER A 1 20260115 "Forsaljning"
{
#TRANS 1930 {} 6250.00
#TRANS 3001 {} -5000.00
#TRANS 2611 {} -1250.00
}
`

// A SIE file with opening balances (#IB) and a voucher imports into an empty
// company: the opening balances are seeded as a verifikat, the voucher is
// replayed, and the resulting ledger balances exactly reflect both.
func TestImportSIEWithOpeningBalances(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")

	exp, err := sie.Parse(strings.NewReader(sampleSIE))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(exp.OpeningBalances) != 2 {
		t.Fatalf("opening balances not parsed: %+v", exp.OpeningBalances)
	}

	res, err := s.ImportSIE(ctx, co, exp)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !res.OpeningPosted || res.Vouchers != 1 {
		t.Fatalf("unexpected import result: %+v", res)
	}

	bal, _ := s.BalancesMap(ctx, co)
	want := map[string]ledger.Amount{
		"1930": ledger.SEK(16250, 0),  // 10000 opening + 6250
		"2081": -ledger.SEK(10000, 0), // equity (credit)
		"3001": -ledger.SEK(5000, 0),  // sales (credit)
		"2611": -ledger.SEK(1250, 0),  // output VAT (credit)
	}
	for acc, w := range want {
		if bal[acc] != w {
			t.Fatalf("account %s = %s, want %s", acc, bal[acc], w)
		}
	}
	assertTrialZero(t, s, co)
}

// An unbalanced voucher aborts the whole import (all-or-nothing transaction).
func TestImportSIEUnbalancedAborts(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()
	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")

	bad := sie.Export{
		Vouchers: []sie.Voucher{{Series: "A", Number: 1, Date: day("2026-01-10"), Text: "bad", Lines: []sie.Line{
			{Account: "1930", Amount: 100_00},
			{Account: "3001", Amount: -99_00},
		}}},
	}
	if _, err := s.ImportSIE(ctx, co, bad); err == nil {
		t.Fatal("expected unbalanced import to fail")
	}
	// Nothing should have been posted.
	sums, _ := s.ListVerificationSummaries(ctx, co)
	if len(sums) != 0 {
		t.Fatalf("import left %d verifikat behind after abort", len(sums))
	}
}
