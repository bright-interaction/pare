// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

// Package invoice models a customer invoice and turns it into a balanced
// verifikat: debit Kundfordringar (1510) for the gross, credit the sales
// account per VAT code for the net, and credit the output-VAT account for the
// moms. VAT is computed per line so both sides of the entry match to the öre.
package invoice

import (
	"time"

	"github.com/bright-interaction/pare/internal/ledger"
	"github.com/bright-interaction/pare/internal/moms"
)

// Line is one invoice row. Quantity is in milli-units (1000 = 1.0) so hours or
// fractional quantities stay exact. UnitPrice is the net price per unit in öre.
type Line struct {
	Description   string
	QuantityMilli int64
	UnitPriceOre  ledger.Amount
	VATCode       moms.Code
}

// Net returns the net line amount in öre (quantity * unit price), rounded to
// the nearest öre half away from zero.
func (l Line) Net() ledger.Amount {
	n := int64(l.UnitPriceOre) * l.QuantityMilli
	if n >= 0 {
		return ledger.Amount((n + 500) / 1000)
	}
	return ledger.Amount((n - 500) / 1000)
}

// VAT returns the output VAT for the line.
func (l Line) VAT() ledger.Amount {
	return moms.VAT(l.Net(), l.VATCode.Rate())
}

// Invoice is a customer invoice. Line amounts are in the invoice currency;
// RatePPM is SEK per 1.00 of that currency in parts per million (1000000 = SEK
// identity). The verifikat is booked in SEK at RatePPM.
type Invoice struct {
	Number   string
	Date     time.Time
	DueDate  time.Time
	Currency string
	RatePPM  int64
	Lines    []Line
}

func (inv Invoice) ratePPM() int64 {
	if inv.RatePPM == 0 {
		return 1_000_000
	}
	return inv.RatePPM
}

// convertToSEK converts an öre amount in the invoice currency to SEK öre at
// ratePPM, rounding to the nearest öre half away from zero.
func convertToSEK(x ledger.Amount, ratePPM int64) ledger.Amount {
	if ratePPM == 0 {
		ratePPM = 1_000_000
	}
	n := int64(x) * ratePPM
	if n >= 0 {
		return ledger.Amount((n + 500_000) / 1_000_000)
	}
	return ledger.Amount((n - 500_000) / 1_000_000)
}

// GrossSEK is the SEK amount booked to Kundfordringar (1510): the sum of the
// per-code net and VAT each converted to SEK. Matches the verifikat exactly.
func (inv Invoice) GrossSEK() ledger.Amount {
	type agg struct{ net, vat ledger.Amount }
	by := map[moms.Code]*agg{}
	for _, l := range inv.Lines {
		a := by[l.VATCode]
		if a == nil {
			a = &agg{}
			by[l.VATCode] = a
		}
		a.net += l.Net()
		a.vat += l.VAT()
	}
	ppm := inv.ratePPM()
	var g ledger.Amount
	for _, a := range by {
		g += convertToSEK(a.net, ppm) + convertToSEK(a.vat, ppm)
	}
	return g
}

// codeOrder keeps the generated verifikat lines deterministic.
var codeOrder = []moms.Code{moms.SE25, moms.SE12, moms.SE06, moms.RCEU, moms.EXP}

// Net is the sum of line net amounts.
func (inv Invoice) Net() ledger.Amount {
	var t ledger.Amount
	for _, l := range inv.Lines {
		t += l.Net()
	}
	return t
}

// VAT is the sum of line VAT amounts.
func (inv Invoice) VAT() ledger.Amount {
	var t ledger.Amount
	for _, l := range inv.Lines {
		t += l.VAT()
	}
	return t
}

// Total is the gross (net + VAT) the customer pays.
func (inv Invoice) Total() ledger.Amount {
	return inv.Net() + inv.VAT()
}

// VerificationLines builds the balanced double-entry lines for the invoice, in
// SEK: each per-code net and VAT is converted at RatePPM, and Kundfordringar
// (1510) is debited with their sum, so the entry balances to the öre.
func (inv Invoice) VerificationLines() []ledger.Line {
	type agg struct{ net, vat ledger.Amount }
	by := map[moms.Code]*agg{}
	for _, l := range inv.Lines {
		a := by[l.VATCode]
		if a == nil {
			a = &agg{}
			by[l.VATCode] = a
		}
		a.net += l.Net()
		a.vat += l.VAT()
	}
	ppm := inv.ratePPM()
	var gross ledger.Amount
	sales := make([]ledger.Line, 0, len(by)*2)
	for _, c := range codeOrder {
		a := by[c]
		if a == nil {
			continue
		}
		netSEK := convertToSEK(a.net, ppm)
		vatSEK := convertToSEK(a.vat, ppm)
		sales = append(sales, ledger.Line{Account: moms.SalesAccountForCode(c), Credit: netSEK, VATCode: string(c)})
		if vatSEK != 0 {
			sales = append(sales, ledger.Line{Account: moms.OutputAccount(c.Rate()), Credit: vatSEK})
		}
		gross += netSEK + vatSEK
	}
	return append([]ledger.Line{{Account: "1510", Debit: gross}}, sales...)
}
