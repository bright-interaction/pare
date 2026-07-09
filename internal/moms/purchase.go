// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package moms

import "github.com/brightinteraction/pare/internal/ledger"

// PurchaseCode is the VAT treatment of a supplier cost line.
type PurchaseCode string

const (
	PD25  PurchaseCode = "PD25"  // domestic purchase, 25% deductible input VAT
	PD12  PurchaseCode = "PD12"  // domestic purchase, 12% input VAT
	PD06  PurchaseCode = "PD06"  // domestic purchase, 6% input VAT
	PEU   PurchaseCode = "PEU"   // services from another EU country, reverse charge 25%
	PIMP  PurchaseCode = "PIMP"  // services from outside the EU, reverse charge 25%
	PNONE PurchaseCode = "PNONE" // no deductible VAT (exempt / private / representation)
)

// PurchaseLabel is a Swedish display label for a purchase VAT treatment.
func (c PurchaseCode) Label() string {
	switch c {
	case PD25:
		return "Svensk moms 25 %"
	case PD12:
		return "Svensk moms 12 %"
	case PD06:
		return "Svensk moms 6 %"
	case PEU:
		return "EU-tjänst, omvänd moms"
	case PIMP:
		return "Tjänst utanför EU, omvänd moms"
	case PNONE:
		return "Ingen avdragsgill moms"
	default:
		return string(c)
	}
}

// PurchaseRate returns the numeric rate a domestic purchase code deducts (0 for
// reverse-charge and no-VAT codes, which self-assess at 25%).
func (c PurchaseCode) domesticRate() (Rate, bool) {
	switch c {
	case PD25:
		return Rate25, true
	case PD12:
		return Rate12, true
	case PD06:
		return Rate6, true
	default:
		return Rate0, false
	}
}

// PurchaseLines builds the balanced verifikat lines for a supplier cost of `net`
// (SEK öre) under the given VAT treatment.
//
//   - Domestic (PD25/PD12/PD06): debit the chosen cost account (net) + 2640
//     ingående moms (VAT), credit 2440 leverantörsskuld (gross).
//   - Reverse charge (PEU/PIMP): the supplier charges no VAT, so we self-assess.
//     Debit BAS 4535 (EU) / 4531 (non-EU) with the net (NOT the operator's cost
//     account, so the momsdeklaration can derive ruta 21/22), credit 2440 (net),
//     and post the fiktiv utgående moms to 2614 and the equal beräknad ingående
//     moms to 2645 (net cash-VAT effect zero).
//   - PNONE: debit the cost account (gross), credit 2440.
func PurchaseLines(costAccount string, net ledger.Amount, code PurchaseCode) []ledger.Line {
	if rate, ok := code.domesticRate(); ok {
		vat := VAT(net, rate)
		return []ledger.Line{
			{Account: costAccount, Debit: net},
			{Account: "2640", Debit: vat},
			{Account: "2440", Credit: net + vat},
		}
	}
	switch code {
	case PEU, PIMP:
		acct := "4535"
		if code == PIMP {
			acct = "4531"
		}
		vat := VAT(net, Rate25)
		return []ledger.Line{
			{Account: acct, Debit: net},
			{Account: "2440", Credit: net},
			{Account: "2614", Credit: vat}, // fiktiv utgående moms -> ruta 30
			{Account: "2645", Debit: vat},  // beräknad ingående moms -> ruta 48
		}
	default: // PNONE
		return []ledger.Line{
			{Account: costAccount, Debit: net},
			{Account: "2440", Credit: net},
		}
	}
}

// Payable returns the amount owed to the supplier (the 2440 credit) for a net
// cost under a VAT treatment: gross incl. deductible input VAT for a domestic
// purchase, and the bare net for reverse-charge (no VAT is invoiced) and no-VAT.
func Payable(net ledger.Amount, code PurchaseCode) ledger.Amount {
	if rate, ok := code.domesticRate(); ok {
		return net + VAT(net, rate)
	}
	return net
}

// ValidPurchaseCode reports whether s is a known purchase VAT treatment.
func ValidPurchaseCode(s string) bool {
	switch PurchaseCode(s) {
	case PD25, PD12, PD06, PEU, PIMP, PNONE:
		return true
	default:
		return false
	}
}
