// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package invoice models a customer invoice and turns it into a balanced
// verifikat: debit Kundfordringar (1510) for the gross, credit the sales
// account per VAT code for the net, and credit the output-VAT account for the
// moms. VAT is computed per line so both sides of the entry match to the öre.
package invoice

import (
	"time"

	"github.com/brightinteraction/pare/internal/ledger"
	"github.com/brightinteraction/pare/internal/moms"
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

// Invoice is a customer invoice.
type Invoice struct {
	Number  string
	Date    time.Time
	DueDate time.Time
	Lines   []Line
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

// VerificationLines builds the balanced double-entry lines for the invoice.
func (inv Invoice) VerificationLines() []ledger.Line {
	type agg struct{ net, vat ledger.Amount }
	by := map[moms.Code]*agg{}
	var gross ledger.Amount
	for _, l := range inv.Lines {
		a := by[l.VATCode]
		if a == nil {
			a = &agg{}
			by[l.VATCode] = a
		}
		net, vat := l.Net(), l.VAT()
		a.net += net
		a.vat += vat
		gross += net + vat
	}
	lines := []ledger.Line{{Account: "1510", Debit: gross}}
	for _, c := range codeOrder {
		a := by[c]
		if a == nil {
			continue
		}
		lines = append(lines, ledger.Line{Account: moms.SalesAccountForCode(c), Credit: a.net, VATCode: string(c)})
		if a.vat != 0 {
			lines = append(lines, ledger.Line{Account: moms.OutputAccount(c.Rate()), Credit: a.vat})
		}
	}
	return lines
}
