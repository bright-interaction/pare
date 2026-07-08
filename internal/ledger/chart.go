// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package ledger

// AccountClass is a BAS kontoklass, derived from the account's first digit.
// This structure is fixed by the BAS standard and is stable across yearly
// chart revisions, so classification never needs the full account list.
type AccountClass int

const (
	ClassUnknown         AccountClass = iota
	ClassAsset                        // 1  Tillgångar
	ClassEquityLiability              // 2  Eget kapital och skulder
	ClassIncome                       // 3  Rörelsens intäkter
	ClassExpense                      // 4-7 Kostnader
	ClassFinancial                    // 8  Finansiella poster
)

// Classify returns the BAS kontoklass for an account number.
func Classify(account string) AccountClass {
	if account == "" {
		return ClassUnknown
	}
	switch account[0] {
	case '1':
		return ClassAsset
	case '2':
		return ClassEquityLiability
	case '3':
		return ClassIncome
	case '4', '5', '6', '7':
		return ClassExpense
	case '8':
		return ClassFinancial
	default:
		return ClassUnknown
	}
}

// IsResult reports whether the class belongs on the resultaträkning.
func (c AccountClass) IsResult() bool {
	return c == ClassIncome || c == ClassExpense || c == ClassFinancial
}

// IsBalance reports whether the class belongs on the balansräkning.
func (c AccountClass) IsBalance() bool {
	return c == ClassAsset || c == ClassEquityLiability
}

// Account is one row of the chart of accounts (kontoplan).
type Account struct {
	Number         string
	Name           string
	DefaultVATCode string
}

// CoreChart is a minimal, well-known subset of BAS accounts sufficient to
// exercise the engine. The full BAS 2025 chart is sourced and seeded in a
// migration once verified against the official list; classification (above)
// does not depend on it.
var CoreChart = []Account{
	{"1510", "Kundfordringar", ""},
	{"1910", "Kassa", ""},
	{"1930", "Företagskonto / bank", ""},
	{"2440", "Leverantörsskulder", ""},
	{"2610", "Utgående moms 25%", "MP1"},
	{"2611", "Utgående moms, omvänd", "MP1"},
	{"2640", "Ingående moms", "MI"},
	{"2650", "Redovisningskonto för moms", ""},
	{"3011", "Försäljning tjänster 25% moms", "MP1"},
	{"4010", "Inköp material och varor", ""},
	{"5010", "Lokalhyra", ""},
	{"6540", "IT-tjänster", ""},
	{"8410", "Räntekostnader", ""},
}
