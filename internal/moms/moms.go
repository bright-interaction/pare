// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

// Package moms computes Swedish VAT: output VAT for a net amount, the BAS
// account mapping, and the momsdeklaration boxes (rutor). Verified against
// skatteverket.se 2026-07-09. All amounts are int64 öre (ledger.Amount).
package moms

import "github.com/bright-interaction/pare/internal/ledger"

// Rate is a Swedish VAT rate in whole percent.
type Rate int

const (
	Rate25 Rate = 25 // standard: services, consulting, IT, most goods
	Rate12 Rate = 12 // hotel, restaurant/catering
	Rate6  Rate = 6  // books, passenger transport, culture; livsmedel from 2026-04-01
	Rate0  Rate = 0  // exempt / outside scope
)

// Code is the VAT treatment of a sales line (stored on the sales account).
type Code string

const (
	SE25 Code = "SE25" // domestic 25%
	SE12 Code = "SE12" // domestic 12%
	SE06 Code = "SE06" // domestic 6%
	RCEU Code = "RCEU" // EU B2B services, reverse charge (no Swedish moms)
	EXP  Code = "EXP"  // services outside the EU (outside scope)
)

// Rate returns the numeric rate a code charges (0 for reverse charge / export).
func (c Code) Rate() Rate {
	switch c {
	case SE25:
		return Rate25
	case SE12:
		return Rate12
	case SE06:
		return Rate6
	default:
		return Rate0
	}
}

// Label is the VAT-summary label for a code (per-rate breakout on the faktura).
func (c Code) Label() string {
	switch c {
	case SE25:
		return "Moms 25 %"
	case SE12:
		return "Moms 12 %"
	case SE06:
		return "Moms 6 %"
	case RCEU:
		return "Omvänd skattskyldighet"
	case EXP:
		return "Export (utanför EU)"
	default:
		return string(c)
	}
}

// LineLabel is the short per-line VAT-column label.
func (c Code) LineLabel() string {
	switch c {
	case RCEU:
		return "Omvänd"
	case EXP:
		return "Export"
	case SE25:
		return "25 %"
	case SE12:
		return "12 %"
	case SE06:
		return "6 %"
	default:
		return "0 %"
	}
}

// LegalNote is the mandatory invoice reference for a special VAT treatment (ML /
// EU VAT Dir art. 226), or "" for a plain domestic line.
func (c Code) LegalNote() string {
	switch c {
	case RCEU:
		return "Omvänd betalningsskyldighet. Reverse charge. Köparen redovisar moms (artikel 196, mervärdesskattedirektivet 2006/112/EG)."
	case EXP:
		return "Omsättning av tjänst utanför EU, ingen svensk moms."
	default:
		return ""
	}
}

// VAT returns the output VAT in öre for a net amount at a rate, rounded to the
// nearest öre (half away from zero).
func VAT(net ledger.Amount, rate Rate) ledger.Amount {
	n := int64(net) * int64(rate)
	if n >= 0 {
		return ledger.Amount((n + 50) / 100)
	}
	return ledger.Amount((n - 50) / 100)
}

// OutputAccount is the BAS utgående-moms account for a domestic rate.
func OutputAccount(rate Rate) string {
	switch rate {
	case Rate25:
		return "2611"
	case Rate12:
		return "2621"
	case Rate6:
		return "2631"
	default:
		return ""
	}
}

// SalesAccount is the BAS domestic sales account for a rate.
func SalesAccount(rate Rate) string {
	switch rate {
	case Rate25:
		return "3001"
	case Rate12:
		return "3002"
	case Rate6:
		return "3003"
	default:
		return ""
	}
}

// SalesAccountForCode is the BAS revenue account for a VAT code, including the
// non-domestic codes: RCEU (EU B2B services, 3308) and EXP (non-EU, 3305).
func SalesAccountForCode(c Code) string {
	switch c {
	case SE25:
		return "3001"
	case SE12:
		return "3002"
	case SE06:
		return "3003"
	case RCEU:
		return "3308"
	case EXP:
		return "3305"
	default:
		return ""
	}
}

// Declaration holds the momsdeklaration boxes Pare computes for a period.
type Declaration struct {
	Box05 ledger.Amount // net domestic taxable sales
	Box10 ledger.Amount // output VAT 25%
	Box11 ledger.Amount // output VAT 12%
	Box12 ledger.Amount // output VAT 6%
	Box20 ledger.Amount // net purchases of goods from another EU country
	Box21 ledger.Amount // net purchases of services from another EU country
	Box22 ledger.Amount // net purchases of services from outside the EU
	Box30 ledger.Amount // self-assessed output 25% on acquisitions above (rutor 20-22)
	Box31 ledger.Amount // self-assessed output 12%
	Box32 ledger.Amount // self-assessed output 6%
	Box39 ledger.Amount // EU B2B services sold (net); needs periodisk sammanställning
	Box48 ledger.Amount // deductible input VAT
	Box49 ledger.Amount // net to pay (positive) or reclaim (negative)
}

// Report derives the declaration boxes from account net balances (debit-positive
// öre, as ledger.Balances / store.TrialBalance produce). Output and sales
// accounts carry credit balances, so they are negated to positive figures;
// purchase accounts carry debit balances, read directly.
func Report(bal map[string]ledger.Amount) Declaration {
	cr := func(acc string) ledger.Amount { return -bal[acc] }
	dr := func(acc string) ledger.Amount { return bal[acc] }
	d := Declaration{
		Box10: cr("2611"),
		Box11: cr("2621"),
		Box12: cr("2631"),
		Box21: dr("4535"), // EU services acquired (reverse charge)
		Box22: dr("4531"), // non-EU services acquired (reverse charge)
		Box30: cr("2614") + cr("2615"),
		Box05: cr("3001") + cr("3002") + cr("3003"),
		Box39: cr("3308"),
		Box48: dr("2640") + dr("2641") + dr("2645"),
	}
	d.Box49 = d.Box10 + d.Box11 + d.Box12 + d.Box30 + d.Box31 + d.Box32 - d.Box48
	return d
}
