// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
package moms

import (
	"testing"

	"github.com/bright-interaction/pare/internal/ledger"
)

func TestVATExact(t *testing.T) {
	cases := []struct {
		net  ledger.Amount
		rate Rate
		want ledger.Amount
	}{
		{ledger.SEK(10000, 0), Rate25, ledger.SEK(2500, 0)},
		{ledger.SEK(100, 0), Rate12, ledger.SEK(12, 0)},
		{ledger.SEK(100, 0), Rate6, ledger.SEK(6, 0)},
		{ledger.Amount(333), Rate25, ledger.Amount(83)},   // 3.33 * 0.25 = 0.8325 -> 83 öre
		{ledger.Amount(-333), Rate25, ledger.Amount(-83)}, // symmetric rounding
	}
	for _, c := range cases {
		if got := VAT(c.net, c.rate); got != c.want {
			t.Errorf("VAT(%s, %d) = %s, want %s", c.net, c.rate, got, c.want)
		}
	}
}

func TestAccounts(t *testing.T) {
	if OutputAccount(Rate25) != "2611" || SalesAccount(Rate25) != "3001" {
		t.Fatal("wrong 25% account mapping")
	}
	if OutputAccount(Rate12) != "2621" || SalesAccount(Rate6) != "3003" {
		t.Fatal("wrong reduced-rate account mapping")
	}
	if SE12.Rate() != Rate12 || RCEU.Rate() != Rate0 {
		t.Fatal("wrong code rate")
	}
}

func TestReport(t *testing.T) {
	// One 25% sale of 10 000 net + a purchase with 500 input VAT.
	bal := map[string]ledger.Amount{
		"3001": -ledger.SEK(10000, 0), // sales credit
		"2611": -ledger.SEK(2500, 0),  // output VAT credit
		"2640": ledger.SEK(500, 0),    // input VAT debit
	}
	d := Report(bal)
	if d.Box05 != ledger.SEK(10000, 0) {
		t.Errorf("Box05 = %s", d.Box05)
	}
	if d.Box10 != ledger.SEK(2500, 0) {
		t.Errorf("Box10 = %s", d.Box10)
	}
	if d.Box48 != ledger.SEK(500, 0) {
		t.Errorf("Box48 = %s", d.Box48)
	}
	if d.Box49 != ledger.SEK(2000, 0) { // 2500 - 500
		t.Errorf("Box49 = %s, want 2000,00", d.Box49)
	}
}
