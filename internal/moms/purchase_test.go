// SPDX-License-Identifier: AGPL-3.0-or-later
package moms

import (
	"testing"

	"github.com/bright-interaction/pare/internal/ledger"
)

func fold(lines []ledger.Line) map[string]ledger.Amount {
	m := map[string]ledger.Amount{}
	for _, l := range lines {
		m[l.Account] += l.Debit - l.Credit
	}
	return m
}

func sum(lines []ledger.Line) ledger.Amount {
	var t ledger.Amount
	for _, l := range lines {
		t += l.Debit - l.Credit
	}
	return t
}

// A service bought from outside the EU (e.g. Anthropic) self-assesses VAT: the
// verifikat balances, no VAT cash is owed (ruta 30 output == ruta 48 input), but
// the purchase net lands in ruta 22.
func TestPurchaseReverseChargeNonEU(t *testing.T) {
	net := ledger.SEK(10000, 0)
	lines := PurchaseLines("", net, PIMP)
	if sum(lines) != 0 {
		t.Fatalf("reverse-charge verifikat not balanced: %s", sum(lines))
	}
	d := Report(fold(lines))
	if d.Box22 != net {
		t.Fatalf("ruta 22 = %s, want %s", d.Box22, net)
	}
	if d.Box30 != ledger.SEK(2500, 0) {
		t.Fatalf("ruta 30 (output) = %s, want 2500", d.Box30)
	}
	if d.Box48 != ledger.SEK(2500, 0) {
		t.Fatalf("ruta 48 (input) = %s, want 2500", d.Box48)
	}
	if d.Box49 != 0 {
		t.Fatalf("ruta 49 (net VAT) = %s, want 0 for reverse charge", d.Box49)
	}
}

// An EU service acquisition lands in ruta 21 instead of 22.
func TestPurchaseReverseChargeEU(t *testing.T) {
	net := ledger.SEK(4000, 0)
	d := Report(fold(PurchaseLines("", net, PEU)))
	if d.Box21 != net {
		t.Fatalf("ruta 21 = %s, want %s", d.Box21, net)
	}
	if d.Box22 != 0 {
		t.Fatalf("ruta 22 should be 0 for an EU service, got %s", d.Box22)
	}
	if d.Box49 != 0 {
		t.Fatalf("net VAT should be 0, got %s", d.Box49)
	}
}

// A domestic purchase deducts input VAT (a reclaim, negative ruta 49) and
// touches no acquisition boxes.
func TestPurchaseDomestic(t *testing.T) {
	net := ledger.SEK(10000, 0)
	lines := PurchaseLines("6540", net, PD25)
	if sum(lines) != 0 {
		t.Fatalf("domestic purchase not balanced: %s", sum(lines))
	}
	d := Report(fold(lines))
	if d.Box48 != ledger.SEK(2500, 0) {
		t.Fatalf("input VAT = %s, want 2500", d.Box48)
	}
	if d.Box49 != -ledger.SEK(2500, 0) {
		t.Fatalf("net VAT = %s, want -2500 (reclaim)", d.Box49)
	}
	if d.Box21 != 0 || d.Box22 != 0 || d.Box30 != 0 {
		t.Fatalf("domestic purchase touched acquisition boxes: 21=%s 22=%s 30=%s", d.Box21, d.Box22, d.Box30)
	}
}
