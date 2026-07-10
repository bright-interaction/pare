// SPDX-License-Identifier: AGPL-3.0-or-later
package invoice

import (
	"testing"
	"time"

	"github.com/bright-interaction/pare/internal/ledger"
	"github.com/bright-interaction/pare/internal/moms"
)

func TestSingleLineVerification(t *testing.T) {
	inv := Invoice{
		Number: "2026-001",
		Date:   time.Now(),
		Lines: []Line{
			{Description: "Konsultarvode", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(10000, 0), VATCode: moms.SE25},
		},
	}
	if inv.Net() != ledger.SEK(10000, 0) || inv.VAT() != ledger.SEK(2500, 0) || inv.Total() != ledger.SEK(12500, 0) {
		t.Fatalf("totals wrong: net=%s vat=%s total=%s", inv.Net(), inv.VAT(), inv.Total())
	}
	lines := inv.VerificationLines()
	v := ledger.Verification{Series: "F", Number: 1, Date: inv.Date, Lines: lines}
	if err := v.Validate(); err != nil {
		t.Fatalf("generated verifikat does not validate: %v", err)
	}
	want := map[string]ledger.Amount{"1510": ledger.SEK(12500, 0), "3001": -ledger.SEK(10000, 0), "2611": -ledger.SEK(2500, 0)}
	got := ledger.Balances([]ledger.Verification{v})
	for acc, w := range want {
		if got[acc] != w {
			t.Errorf("account %s: got %s want %s", acc, got[acc], w)
		}
	}
}

func TestForeignCurrencyBooksSEK(t *testing.T) {
	// 1000.00 EUR net at 25%, rate 11.50 SEK/EUR (ppm 11_500_000)
	inv := Invoice{Currency: "EUR", RatePPM: 11_500_000, Lines: []Line{
		{Description: "Consulting", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(1000, 0), VATCode: moms.SE25},
	}}
	if inv.Net() != ledger.SEK(1000, 0) || inv.VAT() != ledger.SEK(250, 0) {
		t.Fatalf("invoice-currency totals wrong: net=%s vat=%s", inv.Net(), inv.VAT())
	}
	v := ledger.Verification{Series: "F", Number: 1, Date: time.Now(), Lines: inv.VerificationLines()}
	if err := v.Validate(); err != nil {
		t.Fatalf("SEK verifikat unbalanced: %v", err)
	}
	bal := ledger.Balances([]ledger.Verification{v})
	// net 1000 EUR -> 11 500 SEK; vat 250 EUR -> 2 875 SEK; gross 14 375 SEK
	if bal["1510"] != ledger.SEK(14375, 0) {
		t.Errorf("1510 = %s, want 14375,00", bal["1510"])
	}
	if bal["3001"] != -ledger.SEK(11500, 0) {
		t.Errorf("3001 = %s, want -11500,00", bal["3001"])
	}
	if bal["2611"] != -ledger.SEK(2875, 0) {
		t.Errorf("2611 = %s, want -2875,00", bal["2611"])
	}
	if inv.GrossSEK() != ledger.SEK(14375, 0) {
		t.Errorf("GrossSEK = %s, want 14375,00", inv.GrossSEK())
	}
}

func TestMixedRatesBalance(t *testing.T) {
	inv := Invoice{
		Lines: []Line{
			{Description: "Utveckling", QuantityMilli: 7500, UnitPriceOre: ledger.SEK(1200, 0), VATCode: moms.SE25}, // 7.5h * 1200
			{Description: "Bok", QuantityMilli: 3000, UnitPriceOre: ledger.SEK(249, 0), VATCode: moms.SE06},
			{Description: "EU SaaS-konsult", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(5000, 0), VATCode: moms.RCEU},
		},
	}
	v := ledger.Verification{Series: "F", Number: 2, Date: time.Now(), Lines: inv.VerificationLines()}
	if err := v.Validate(); err != nil {
		t.Fatalf("mixed-rate verifikat unbalanced: %v", err)
	}
	// reverse-charge line: revenue on 3308, no output VAT
	bal := ledger.Balances([]ledger.Verification{v})
	if bal["3308"] != -ledger.SEK(5000, 0) {
		t.Errorf("3308 = %s, want -5000,00", bal["3308"])
	}
	// gross debit on 1510 equals net + vat
	if bal["1510"] != inv.Total() {
		t.Errorf("1510 debit %s != invoice total %s", bal["1510"], inv.Total())
	}
}
