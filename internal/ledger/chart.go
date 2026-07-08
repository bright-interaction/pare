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

// CoreChart is the essential BAS chart of accounts for a Swedish service AB,
// with official names verified against bas.se (BAS 2025/2026) 2026-07-09. It is
// what BootstrapCompany seeds. VAT codes on sales accounts drive the moms
// engine. Classification (above) works for any account, chart or not.
var CoreChart = []Account{
	// Klass 1 Tillgångar
	{"1510", "Kundfordringar", ""},
	{"1910", "Kassa", ""},
	{"1930", "Företagskonto/checkkonto/affärskonto", ""},
	// Klass 2 Eget kapital och skulder
	{"2081", "Aktiekapital", ""},
	{"2091", "Balanserad vinst eller förlust", ""},
	{"2099", "Årets resultat", ""},
	{"2440", "Leverantörsskulder", ""},
	{"2611", "Utgående moms på försäljning inom Sverige, 25 %", ""},
	{"2621", "Utgående moms på försäljning inom Sverige, 12 %", ""},
	{"2631", "Utgående moms på försäljning inom Sverige, 6 %", ""},
	{"2614", "Utgående moms, omvänd betalningsskyldighet, 25 %", ""},
	{"2615", "Utgående moms import av varor, 25 %", ""},
	{"2640", "Ingående moms", ""},
	{"2641", "Debiterad ingående moms", ""},
	{"2645", "Beräknad ingående moms på förvärv från utlandet", ""},
	{"2650", "Redovisningskonto för moms", ""},
	// Klass 3 Rörelsens intäkter
	{"3001", "Försäljning inom Sverige, 25 % moms", "SE25"},
	{"3002", "Försäljning inom Sverige, 12 % moms", "SE12"},
	{"3003", "Försäljning inom Sverige, 6 % moms", "SE06"},
	{"3305", "Försäljning tjänster till land utanför EU", "EXP"},
	{"3308", "Försäljning tjänster till annat EU-land", "RCEU"},
	// Klass 4 Kostnader för varor och köpta tjänster
	{"4531", "Inköp av tjänster från ett land utanför EU, 25 % moms", ""},
	{"4535", "Inköp av tjänster från annat EU-land, 25 % moms", ""},
	// Klass 5 Övriga externa kostnader (del 1)
	{"5010", "Lokalhyra", ""},
	{"5410", "Förbrukningsinventarier", ""},
	{"5460", "Förbrukningsmaterial", ""},
	{"5910", "Annonsering", ""},
	// Klass 6 Övriga externa kostnader (del 2)
	{"6110", "Kontorsmateriel", ""},
	{"6212", "Mobiltelefon", ""},
	{"6230", "Datakommunikation", ""},
	{"6310", "Företagsförsäkringar", ""},
	{"6530", "Redovisningstjänster", ""},
	{"6540", "IT-tjänster", ""},
	{"6550", "Konsultarvoden", ""},
	{"6570", "Bankkostnader", ""},
	// Klass 7 Personalkostnader
	{"7210", "Löner till tjänstemän", ""},
	{"7220", "Löner till företagsledare", ""},
	{"7510", "Arbetsgivaravgifter", ""},
	{"7690", "Övriga personalkostnader", ""},
	// Klass 8 Finansiella poster och årets resultat
	{"8310", "Ränteintäkter från omsättningstillgångar", ""},
	{"8410", "Räntekostnader för långfristiga skulder", ""},
	{"8420", "Räntekostnader för kortfristiga skulder", ""},
	{"8910", "Skatt som belastar årets resultat", ""},
	{"8999", "Årets resultat", ""},
}
