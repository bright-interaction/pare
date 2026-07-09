// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package ledger

import (
	"sort"
	"strings"
)

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

// StatementRow is one account line on a financial statement. Amount carries the
// presentation sign: always positive on the account's natural side (income,
// equity and liabilities shown positive though they are credit-balances; assets
// and expenses shown positive as debit-balances).
type StatementRow struct {
	Account string
	Name    string
	Class   AccountClass
	Amount  Amount
}

// Statements is the resultaträkning and balansräkning derived from a trial
// balance. The balansräkning includes a synthetic "årets resultat" equity line
// (the live period result, not yet closed to equity) so total assets equal
// total equity plus liabilities.
type Statements struct {
	// Resultaträkning
	Income         []StatementRow
	Expenses       []StatementRow
	Financial      []StatementRow
	IncomeTotal    Amount
	ExpenseTotal   Amount
	FinancialTotal Amount // financial net income (credit-positive; can be negative)
	Result         Amount // profit positive, loss negative

	// Balansräkning
	Assets         []StatementRow
	Equity         []StatementRow
	Liabilities    []StatementRow
	AssetTotal     Amount
	EquityTotal    Amount // includes the synthetic result row
	LiabilityTotal Amount
}

// ResultOf returns the profit/loss implied by a trial balance's result classes.
func ResultOf(tb []AccountBalance) Amount {
	var net Amount
	for _, r := range tb {
		if r.Class.IsResult() {
			net += r.Net
		}
	}
	return -net
}

// BuildStatements groups a single trial balance into financial statements
// (resultaträkning + balansräkning from the same balances). nameFn resolves an
// account number to its display name (pass nil to skip names).
func BuildStatements(tb []AccountBalance, nameFn func(account string) string) Statements {
	return BuildStatementsPeriod(tb, tb, ResultOf(tb), nameFn)
}

// BuildStatementsPeriod builds statements for a period: the resultaträkning
// (income/expense/financial) comes from plTB (a period slice), the balansräkning
// (assets/equity/liabilities) from bsTB (cumulative as of the period end), and
// the synthetic "årets resultat" equity row from yearResult (the fiscal
// year-to-date result), so the balance sheet balances as of the period end.
func BuildStatementsPeriod(plTB, bsTB []AccountBalance, yearResult Amount, nameFn func(account string) string) Statements {
	name := func(acc string) string {
		if nameFn == nil {
			return ""
		}
		return nameFn(acc)
	}
	var s Statements
	for _, r := range plTB {
		row := StatementRow{Account: r.Account, Name: name(r.Account), Class: r.Class}
		switch r.Class {
		case ClassIncome: // credit-positive
			row.Amount = -r.Net
			s.Income = append(s.Income, row)
			s.IncomeTotal += row.Amount
		case ClassExpense: // debit-positive
			row.Amount = r.Net
			s.Expenses = append(s.Expenses, row)
			s.ExpenseTotal += row.Amount
		case ClassFinancial: // mixed; present credit-positive (income minus cost)
			row.Amount = -r.Net
			s.Financial = append(s.Financial, row)
			s.FinancialTotal += row.Amount
		}
	}
	s.Result = s.IncomeTotal - s.ExpenseTotal + s.FinancialTotal

	for _, r := range bsTB {
		row := StatementRow{Account: r.Account, Name: name(r.Account), Class: r.Class}
		switch r.Class {
		case ClassAsset: // debit-positive
			row.Amount = r.Net
			s.Assets = append(s.Assets, row)
			s.AssetTotal += row.Amount
		case ClassEquityLiability: // credit-positive
			row.Amount = -r.Net
			if strings.HasPrefix(r.Account, "20") { // eget kapital
				s.Equity = append(s.Equity, row)
				s.EquityTotal += row.Amount
			} else { // skulder
				s.Liabilities = append(s.Liabilities, row)
				s.LiabilityTotal += row.Amount
			}
		}
	}

	// Fold the fiscal year-to-date result into equity so the balansräkning
	// balances. Not a posted account: it is the calculated result before
	// year-end close.
	if yearResult != 0 {
		s.Equity = append(s.Equity, StatementRow{
			Account: "", Name: "Årets resultat (beräknat)",
			Class: ClassEquityLiability, Amount: yearResult,
		})
		s.EquityTotal += yearResult
	}
	return s
}
