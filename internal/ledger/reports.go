// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package ledger

import "sort"

// Balances sums net (debit minus credit) per account across the given
// verifications. In BAS, asset and expense accounts trend debit-positive;
// equity, liability and income accounts trend credit-positive (negative here).
func Balances(vs []Verification) map[string]Amount {
	m := map[string]Amount{}
	for _, v := range vs {
		for _, l := range v.Lines {
			m[l.Account] += l.Debit - l.Credit
		}
	}
	return m
}

// AccountBalance is one row of a trial balance.
type AccountBalance struct {
	Account string
	Class   AccountClass
	Net     Amount // debit-positive
}

// TrialBalance returns per-account net balances sorted by account number. For a
// consistent set of balanced verifications the sum of all Net values is zero.
func TrialBalance(vs []Verification) []AccountBalance {
	m := Balances(vs)
	out := make([]AccountBalance, 0, len(m))
	for acc, net := range m {
		out = append(out, AccountBalance{Account: acc, Class: Classify(acc), Net: net})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Account < out[j].Account })
	return out
}

// Result returns the period profit (positive) or loss (negative): income minus
// expenses across the resultaträkning accounts.
func Result(vs []Verification) Amount {
	var net Amount
	for acc, bal := range Balances(vs) {
		if Classify(acc).IsResult() {
			net += bal
		}
	}
	// Income accounts are credit-heavy (net negative), expenses debit-heavy
	// (net positive), so profit is the negation of the summed net.
	return -net
}
