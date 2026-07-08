// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package moms computes Swedish VAT: output VAT for a net amount, the BAS
// account mapping, and the momsdeklaration boxes (rutor). Verified against
// skatteverket.se 2026-07-09. All amounts are int64 öre (ledger.Amount).
package moms

import "github.com/brightinteraction/pare/internal/ledger"

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
	Box30 ledger.Amount // self-assessed output 25% (reverse-charge acquisitions)
	Box39 ledger.Amount // EU B2B services sold (net); needs periodisk sammanställning
	Box48 ledger.Amount // deductible input VAT
	Box49 ledger.Amount // net to pay (positive) or reclaim (negative)
}

// Report derives the declaration boxes from account net balances (debit-positive
// öre, as ledger.Balances / store.TrialBalance produce). Output and sales
// accounts carry credit balances, so they are negated to positive figures.
func Report(bal map[string]ledger.Amount) Declaration {
	cr := func(acc string) ledger.Amount { return -bal[acc] }
	dr := func(acc string) ledger.Amount { return bal[acc] }
	d := Declaration{
		Box10: cr("2611"),
		Box11: cr("2621"),
		Box12: cr("2631"),
		Box30: cr("2614"),
		Box05: cr("3001") + cr("3002") + cr("3003"),
		Box39: cr("3308"),
		Box48: dr("2640") + dr("2641") + dr("2645"),
	}
	d.Box49 = d.Box10 + d.Box11 + d.Box12 + d.Box30 - d.Box48
	return d
}
