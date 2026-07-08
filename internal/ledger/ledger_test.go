// SPDX-License-Identifier: AGPL-3.0-or-later
package ledger

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

// sale: bank 12 500 debit; sales 10 000 credit; utgående moms 2 500 credit.
func sale() Verification {
	return Verification{
		Series: "A", Number: 1, Date: day("2026-01-15"), Description: "Konsultarvode",
		Lines: []Line{
			{Account: "1930", Debit: SEK(12500, 0)},
			{Account: "3011", Credit: SEK(10000, 0), VATCode: "MP1"},
			{Account: "2610", Credit: SEK(2500, 0)},
		},
	}
}

// cost: hyra 2 000 debit; ingående moms 500 debit; bank 2 500 credit.
func cost() Verification {
	return Verification{
		Series: "A", Number: 2, Date: day("2026-01-20"), Description: "Lokalhyra",
		Lines: []Line{
			{Account: "5010", Debit: SEK(2000, 0)},
			{Account: "2640", Debit: SEK(500, 0)},
			{Account: "1930", Credit: SEK(2500, 0)},
		},
	}
}

func TestValidateBalanced(t *testing.T) {
	for _, v := range []Verification{sale(), cost()} {
		if err := v.Validate(); err != nil {
			t.Fatalf("%s: unexpected error %v", v.ID(), err)
		}
	}
}

func TestValidateUnbalanced(t *testing.T) {
	v := sale()
	v.Lines[0].Debit = SEK(9999, 0) // break the balance
	if err := v.Validate(); err != ErrUnbalanced {
		t.Fatalf("want ErrUnbalanced, got %v", err)
	}
}

func TestValidateLineSide(t *testing.T) {
	v := sale()
	v.Lines[0].Credit = SEK(1, 0) // line now has both debit and credit
	if err := v.Validate(); err != ErrLineSide {
		t.Fatalf("want ErrLineSide, got %v", err)
	}
}

func TestTrialBalanceSumsToZero(t *testing.T) {
	tb := TrialBalance([]Verification{sale(), cost()})
	var total Amount
	for _, r := range tb {
		total += r.Net
	}
	if total != 0 {
		t.Fatalf("trial balance does not net to zero: %s", total)
	}
}

func TestResultIsProfit(t *testing.T) {
	got := Result([]Verification{sale(), cost()})
	want := SEK(8000, 0) // 10 000 income - 2 000 expense
	if got != want {
		t.Fatalf("Result = %s, want %s", got, want)
	}
}

func TestReverseCancelsOriginal(t *testing.T) {
	orig := sale()
	rev := Reverse(orig, "A", 99, day("2026-02-01"), "")
	if err := rev.Validate(); err != nil {
		t.Fatalf("reversal invalid: %v", err)
	}
	if rev.ReversalOf != orig.ID() {
		t.Fatalf("ReversalOf = %q, want %q", rev.ReversalOf, orig.ID())
	}
	for acc, net := range Balances([]Verification{orig, rev}) {
		if net != 0 {
			t.Fatalf("account %s not cancelled: net %s", acc, net)
		}
	}
}
