// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	gen "github.com/brightinteraction/pare/internal/db/generated"
	"github.com/brightinteraction/pare/internal/ledger"
)

// ErrNotFinalized is returned when recording a payment on a non-finalized invoice.
var ErrNotFinalized = errors.New("store: invoice is not finalized")

// ErrPaymentMismatch is returned when a SEK invoice is settled with an amount
// that differs from the invoice total (which would otherwise be mis-booked as a
// currency difference).
var ErrPaymentMismatch = errors.New("store: payment amount must equal the invoice total for a SEK invoice")

// ErrInvoiceNotFound is returned when no invoice matches the given number.
var ErrInvoiceNotFound = errors.New("store: no invoice with that number")

// RecordPaymentByNumber resolves an invoice by its (company-scoped) number and
// settles it. This is the reconciliation entry point for the MCP, where the AI
// references invoices by their human number, never by internal UUID.
func (s *Store) RecordPaymentByNumber(ctx context.Context, companyID uuid.UUID, number string, date time.Time, account string, paymentSEK ledger.Amount) (uuid.UUID, error) {
	dbInv, err := s.q.GetInvoiceByNumber(ctx, gen.GetInvoiceByNumberParams{CompanyID: companyID, Number: number})
	if err != nil {
		return uuid.Nil, ErrInvoiceNotFound
	}
	return s.RecordPayment(ctx, companyID, dbInv.ID, date, account, paymentSEK)
}

// RecordPayment settles a finalized invoice: debit the bank account with the SEK
// actually received, credit Kundfordringar (1510) with the booked receivable,
// and post any exchange difference to 3960 (gain) or 7960 (loss). Posts a
// series-B verifikat and marks the invoice paid, in one transaction.
func (s *Store) RecordPayment(ctx context.Context, companyID, invoiceID uuid.UUID, date time.Time, account string, paymentSEK ledger.Amount) (uuid.UUID, error) {
	dbInv, err := s.q.GetInvoice(ctx, invoiceID)
	if err != nil {
		return uuid.Nil, err
	}
	if dbInv.CompanyID != companyID {
		return uuid.Nil, ErrForeignCompany
	}
	if dbInv.Status != "finalized" {
		return uuid.Nil, ErrNotFinalized
	}
	if paymentSEK <= 0 {
		return uuid.Nil, ErrPaymentMismatch
	}
	view, err := s.InvoiceForRender(ctx, companyID, invoiceID)
	if err != nil {
		return uuid.Nil, err
	}
	gross := view.TotalSEK // the SEK receivable booked to 1510 at finalize

	lines := []ledger.Line{
		{Account: account, Debit: paymentSEK},
		{Account: "1510", Credit: paymentSEK},
	}
	fullyPaid := false

	if dbInv.Currency != "SEK" {
		// Foreign-currency invoices settle in a single payment; the SEK received
		// vs the SEK booked at the finalize rate is a kursdifferens. (Partial
		// payments of a foreign invoice are not supported.)
		if ledger.Amount(dbInv.AmountPaidOre) != 0 {
			return uuid.Nil, ErrPaymentMismatch
		}
		lines[1].Credit = gross // clear the whole receivable
		switch diff := paymentSEK - gross; {
		case diff > 0:
			lines = append(lines, ledger.Line{Account: "3960", Credit: diff}) // valutakursvinst
		case diff < 0:
			lines = append(lines, ledger.Line{Account: "7960", Debit: -diff}) // valutakursförlust
		}
		fullyPaid = true
	} else {
		// SEK: support instalments and öresavrundning. residual is the receivable
		// remaining after this payment.
		newPaid := ledger.Amount(dbInv.AmountPaidOre) + paymentSEK
		residual := gross - newPaid
		switch {
		case residual == 0:
			fullyPaid = true
		case absAmount(residual) < oresRoundingThreshold:
			// Close the small difference to 3740 (öres- och kronutjämning).
			if residual > 0 { // slightly underpaid: write off as a tiny cost
				lines = append(lines,
					ledger.Line{Account: "1510", Credit: residual},
					ledger.Line{Account: "3740", Debit: residual})
			} else { // slightly overpaid: tiny income
				r := -residual
				lines = append(lines,
					ledger.Line{Account: "1510", Debit: r},
					ledger.Line{Account: "3740", Credit: r})
			}
			fullyPaid = true
		case residual < 0:
			// Overpaid beyond the öre tolerance: that is a refund, not a payment.
			return uuid.Nil, ErrPaymentMismatch
		default:
			// residual > tolerance: a genuine partial payment; invoice stays open.
		}
	}

	var verID uuid.UUID
	err = s.inTx(ctx, func(qtx *gen.Queries) error {
		id, err := s.postVerification(ctx, qtx, companyID, "B", date, "Betalning faktura "+dbInv.Number, lines, uuid.Nil)
		if err != nil {
			return err
		}
		verID = id
		if err := qtx.AddInvoicePayment(ctx, gen.AddInvoicePaymentParams{ID: invoiceID, AmountPaidOre: int64(paymentSEK), CompanyID: companyID}); err != nil {
			return err
		}
		if fullyPaid {
			// The status='finalized' guard serializes concurrent final payments:
			// only one flips finalized->paid, the loser sees 0 rows and rolls back.
			n, err := qtx.MarkInvoicePaid(ctx, gen.MarkInvoicePaidParams{
				ID:                    invoiceID,
				PaidAt:                pgDate(date),
				PaymentVerificationID: pgUUID(id),
				CompanyID:             companyID,
			})
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrNotFinalized
			}
		}
		return s.logAudit(ctx, qtx, companyID, "record_payment", "invoice", invoiceID.String(), dbInv.Number+" "+paymentSEK.String()+" SEK")
	})
	return verID, err
}

// oresRoundingThreshold: a final payment within this of the balance settles the
// invoice, with the difference booked to 3740 (under 1 SEK).
const oresRoundingThreshold = ledger.Amount(100)

func absAmount(a ledger.Amount) ledger.Amount {
	if a < 0 {
		return -a
	}
	return a
}
