// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	gen "github.com/bright-interaction/pare/internal/db/generated"
	"github.com/bright-interaction/pare/internal/invoice"
	"github.com/bright-interaction/pare/internal/ledger"
	"github.com/bright-interaction/pare/internal/moms"
	"github.com/bright-interaction/pare/internal/render"
	"github.com/bright-interaction/pare/internal/sie"
)

// ErrNotDraft is returned when finalizing an invoice that is not a draft.
var ErrNotDraft = errors.New("store: invoice is not a draft")

// CreateInvoice inserts a draft invoice and its lines. The counterparty must
// belong to the company.
func (s *Store) CreateInvoice(ctx context.Context, companyID, counterpartyID uuid.UUID, inv invoice.Invoice) (uuid.UUID, error) {
	cp, err := s.q.GetCounterparty(ctx, counterpartyID)
	if err != nil {
		return uuid.Nil, err
	}
	if cp.CompanyID != companyID {
		return uuid.Nil, ErrForeignCompany
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	qtx := s.q.WithTx(tx)

	currency := inv.Currency
	if currency == "" {
		currency = "SEK"
	}
	ratePPM := inv.RatePPM
	if ratePPM == 0 {
		ratePPM = 1_000_000
	}
	row, err := qtx.InsertInvoice(ctx, gen.InsertInvoiceParams{
		CompanyID:      companyID,
		CounterpartyID: counterpartyID,
		InvoiceDate:    pgDateOrNull(inv.Date),
		DueDate:        pgDateOrNull(inv.DueDate),
		Currency:       currency,
		RatePpm:        ratePPM,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: insert invoice: %w", err)
	}
	for i, l := range inv.Lines {
		if err := qtx.InsertInvoiceLine(ctx, gen.InsertInvoiceLineParams{
			InvoiceID:     row.ID,
			LineNo:        int32(i + 1),
			Description:   l.Description,
			QuantityMilli: l.QuantityMilli,
			UnitPriceOre:  int64(l.UnitPriceOre),
			VatCode:       string(l.VATCode),
		}); err != nil {
			return uuid.Nil, fmt.Errorf("store: insert invoice line: %w", err)
		}
	}
	if err := s.logAudit(ctx, qtx, companyID, "create_invoice", "invoice", row.ID.String(), inv.Total().String()); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return row.ID, nil
}

// FinalizeInvoice assigns a number, auto-posts the balanced verifikat (series F)
// and marks the invoice finalized, all in one transaction. Returns the posted
// verification id.
// FinalizeInvoice numbers a draft invoice (allocating a gap-free per-year
// number inside the transaction), auto-posts its balanced verifikat, and marks
// it finalized. It returns the verifikat id and the allocated invoice number.
func (s *Store) FinalizeInvoice(ctx context.Context, companyID, invoiceID uuid.UUID, date, due time.Time) (uuid.UUID, string, error) {
	dbInv, err := s.q.GetInvoice(ctx, invoiceID)
	if err != nil {
		return uuid.Nil, "", err
	}
	if dbInv.CompanyID != companyID {
		return uuid.Nil, "", ErrForeignCompany
	}
	if dbInv.Status != "draft" {
		return uuid.Nil, "", ErrNotDraft
	}
	lineRows, err := s.q.ListInvoiceLines(ctx, invoiceID)
	if err != nil {
		return uuid.Nil, "", err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, "", err
	}
	defer tx.Rollback(ctx)
	qtx := s.q.WithTx(tx)

	// Allocate the number inside the tx so it is gap-free, per-year, and race-safe
	// (a rollback below returns the number to the pool).
	seq, err := qtx.AllocInvoiceNumber(ctx, gen.AllocInvoiceNumberParams{CompanyID: companyID, Year: int32(date.Year())})
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("store: allocate invoice number: %w", err)
	}
	number := fmt.Sprintf("%d-%04d", date.Year(), seq)

	inv := invoice.Invoice{Number: number, Date: date, DueDate: due, Currency: dbInv.Currency, RatePPM: dbInv.RatePpm}
	for _, r := range lineRows {
		inv.Lines = append(inv.Lines, invoice.Line{
			Description:   r.Description,
			QuantityMilli: r.QuantityMilli,
			UnitPriceOre:  ledger.Amount(r.UnitPriceOre),
			VATCode:       moms.Code(r.VatCode),
		})
	}

	verID, err := s.postVerification(ctx, qtx, companyID, "F", date, "Faktura "+number, inv.VerificationLines(), uuid.Nil)
	if err != nil {
		return uuid.Nil, "", err
	}
	if err := qtx.FinalizeInvoice(ctx, gen.FinalizeInvoiceParams{
		ID:             invoiceID,
		Number:         number,
		InvoiceDate:    pgDate(date),
		DueDate:        pgDateOrNull(due),
		VerificationID: pgUUID(verID),
		CompanyID:      companyID,
	}); err != nil {
		return uuid.Nil, "", fmt.Errorf("store: finalize invoice: %w", err)
	}
	if err := s.logAudit(ctx, qtx, companyID, "finalize_invoice", "invoice", invoiceID.String(), number+" "+inv.GrossSEK().String()+" SEK"); err != nil {
		return uuid.Nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, "", err
	}
	return verID, number, nil
}

// ExportSIE builds a SIE type 4 export from the company, its chart and all
// posted verifications. The fiscal year is derived from the earliest voucher
// (single-year V1; multi-year export is deferred).
func (s *Store) ExportSIE(ctx context.Context, companyID uuid.UUID, generated time.Time) (sie.Export, error) {
	co, err := s.q.GetCompany(ctx, companyID)
	if err != nil {
		return sie.Export{}, err
	}
	accts, err := s.q.ListAccounts(ctx, companyID)
	if err != nil {
		return sie.Export{}, err
	}
	vers, err := s.q.ListVerifications(ctx, companyID)
	if err != nil {
		return sie.Export{}, err
	}
	lineRows, err := s.q.ListLinesForCompany(ctx, companyID)
	if err != nil {
		return sie.Export{}, err
	}

	byVer := map[uuid.UUID][]sie.Line{}
	for _, r := range lineRows {
		byVer[r.VerificationID] = append(byVer[r.VerificationID], sie.Line{
			Account: r.Account,
			Amount:  r.DebitOre - r.CreditOre,
		})
	}

	exp := sie.Export{CompanyName: co.Name, OrgNr: co.Orgnr, Generated: generated}
	for _, a := range accts {
		exp.Accounts = append(exp.Accounts, sie.Account{Number: a.Number, Name: a.Name})
	}
	for _, v := range vers {
		exp.Vouchers = append(exp.Vouchers, sie.Voucher{
			Series: v.Series,
			Number: int(v.Number),
			Date:   v.Vdate.Time,
			Text:   v.Description,
			Lines:  byVer[v.ID],
		})
	}
	y := generated.Year()
	if len(vers) > 0 {
		y = vers[0].Vdate.Time.Year()
	}
	yearStart := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
	yearEnd := time.Date(y, 12, 31, 0, 0, 0, 0, time.UTC)
	exp.YearStart = yearStart
	exp.YearEnd = yearEnd

	// #IB (opening = balance accounts carried in before the year), #UB (closing =
	// balance accounts at year end), #RES (result accounts over the year).
	if ib, err := s.TrialBalanceAsOf(ctx, companyID, yearStart.AddDate(0, 0, -1)); err == nil {
		for _, r := range ib {
			if r.Class.IsBalance() && r.Net != 0 {
				exp.OpeningBalances = append(exp.OpeningBalances, sie.Balance{Account: r.Account, Amount: int64(r.Net)})
			}
		}
	}
	if ub, err := s.TrialBalanceAsOf(ctx, companyID, yearEnd); err == nil {
		for _, r := range ub {
			if r.Class.IsBalance() && r.Net != 0 {
				exp.ClosingBalances = append(exp.ClosingBalances, sie.Balance{Account: r.Account, Amount: int64(r.Net)})
			}
		}
	}
	if res, err := s.TrialBalanceBetween(ctx, companyID, yearStart, yearEnd); err == nil {
		for _, r := range res {
			if r.Class.IsResult() && r.Net != 0 {
				exp.Results = append(exp.Results, sie.Balance{Account: r.Account, Amount: int64(r.Net)})
			}
		}
	}
	return exp, nil
}

// DeleteDraftInvoice removes a draft invoice (and its lines, via cascade). Only
// drafts can be deleted; finalized invoices are immutable accounting records.
func (s *Store) DeleteDraftInvoice(ctx context.Context, companyID, invoiceID uuid.UUID) error {
	n, err := s.q.DeleteDraftInvoice(ctx, gen.DeleteDraftInvoiceParams{ID: invoiceID, CompanyID: companyID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotDraft
	}
	return s.logAudit(ctx, s.q, companyID, "delete_draft_invoice", "invoice", invoiceID.String(), "")
}

// CreditInvoice issues a kreditfaktura for a finalized invoice: it creates a new
// invoice whose lines negate the original, finalizes it (posting a reversing
// verifikat), links it to the original, and marks the original cancelled. Returns
// the credit note's id and number.
func (s *Store) CreditInvoice(ctx context.Context, companyID, invoiceID uuid.UUID) (uuid.UUID, string, error) {
	orig, err := s.q.GetInvoice(ctx, invoiceID)
	if err != nil {
		return uuid.Nil, "", err
	}
	if orig.CompanyID != companyID {
		return uuid.Nil, "", ErrForeignCompany
	}
	// A finalized (unpaid) invoice or a paid one (a refund) can be credited.
	if orig.Status != "finalized" && orig.Status != "paid" {
		return uuid.Nil, "", ErrNotFinalized
	}
	origLines, err := s.q.ListInvoiceLines(ctx, invoiceID)
	if err != nil {
		return uuid.Nil, "", err
	}
	// The credit-note invoice row carries the same (positive) lines so its PDF
	// shows the credited amounts; it is labelled Kreditfaktura via credits_invoice_id.
	credit := invoice.Invoice{Currency: orig.Currency, RatePPM: orig.RatePpm}
	for _, r := range origLines {
		credit.Lines = append(credit.Lines, invoice.Line{
			Description:   r.Description,
			QuantityMilli: r.QuantityMilli,
			UnitPriceOre:  ledger.Amount(r.UnitPriceOre),
			VATCode:       moms.Code(r.VatCode),
		})
	}
	creditID, err := s.CreateInvoice(ctx, companyID, orig.CounterpartyID, credit)
	if err != nil {
		return uuid.Nil, "", err
	}
	if err := s.q.SetInvoiceCredits(ctx, gen.SetInvoiceCreditsParams{ID: creditID, CreditsInvoiceID: pgUUID(invoiceID)}); err != nil {
		return uuid.Nil, "", err
	}

	// The credit-note verifikat REVERSES the original's (swap debit/credit, all
	// amounts stay non-negative), so the books return to their pre-invoice state.
	origVerLines, err := s.q.ListVerificationLinesByVerification(ctx, uuidFromPg(orig.VerificationID))
	if err != nil {
		return uuid.Nil, "", err
	}
	revLines := make([]ledger.Line, 0, len(origVerLines))
	for _, l := range origVerLines {
		revLines = append(revLines, ledger.Line{
			Account: l.Account, Debit: ledger.Amount(l.CreditOre), Credit: ledger.Amount(l.DebitOre), VATCode: l.VatCode,
		})
	}

	date := time.Now()
	var number string
	err = s.inTx(ctx, func(qtx *gen.Queries) error {
		seq, err := qtx.AllocInvoiceNumber(ctx, gen.AllocInvoiceNumberParams{CompanyID: companyID, Year: int32(date.Year())})
		if err != nil {
			return err
		}
		number = fmt.Sprintf("%d-%04d", date.Year(), seq)
		verID, err := s.postVerification(ctx, qtx, companyID, "F", date, "Kreditfaktura "+number+" avser "+orig.Number, revLines, uuidFromPg(orig.VerificationID))
		if err != nil {
			return err
		}
		if err := qtx.FinalizeInvoice(ctx, gen.FinalizeInvoiceParams{
			ID: creditID, Number: number, InvoiceDate: pgDate(date), DueDate: pgDateOrNull(date),
			VerificationID: pgUUID(verID), CompanyID: companyID,
		}); err != nil {
			return err
		}
		if _, err := qtx.MarkInvoiceCredited(ctx, gen.MarkInvoiceCreditedParams{ID: invoiceID, CompanyID: companyID}); err != nil {
			return err
		}
		return s.logAudit(ctx, qtx, companyID, "credit_invoice", "invoice", invoiceID.String(), "kreditfaktura "+number)
	})
	if err != nil {
		return uuid.Nil, "", err
	}
	return creditID, number, nil
}

// InvoiceView is a fully-resolved invoice for rendering (customer decrypted,
// amounts computed). It is what the PDF template and the operator UI consume.
type InvoiceView struct {
	Number           string
	Status           string
	Date             time.Time
	DueDate          time.Time
	CompanyName      string
	CompanyOrgNr     string
	CompanyMomsRegNr string
	CompanyAddress   string
	CompanyPostal    string // "111 22 Stockholm"
	CompanyFSkatt    bool
	Bankgiro         string
	IBAN             string
	Customer         Counterparty
	Lines            []InvoiceViewLine
	VATSummary       []VATSummaryRow // net + VAT per rate/treatment (legal breakout)
	LegalNotes       []string        // reverse-charge / export references
	Net              ledger.Amount
	VAT              ledger.Amount
	Total            ledger.Amount
	Currency         string
	TotalSEK         ledger.Amount // gross booked to Kundfordringar in SEK
	AmountPaid       ledger.Amount // SEK settled so far (partial payments)
	OCR              string
	IsCredit         bool   // this invoice is a kreditfaktura
	CreditsNumber    string // the original invoice number it credits
}

// Outstanding returns the SEK still owed on the invoice.
func (v InvoiceView) Outstanding() ledger.Amount { return v.TotalSEK - v.AmountPaid }

// VATSummaryRow is one row of the per-rate VAT breakout required on a faktura.
type VATSummaryRow struct {
	Label string
	Net   ledger.Amount
	VAT   ledger.Amount
}

// InvoiceViewLine is a rendered invoice line.
type InvoiceViewLine struct {
	Description   string
	QuantityMilli int64
	UnitPrice     ledger.Amount
	VATLabel      string // "25 %", "Omvänd", "Export"
	Net           ledger.Amount
	VAT           ledger.Amount
}

// InvoiceForRender loads an invoice, decrypts the customer, and computes totals.
func (s *Store) InvoiceForRender(ctx context.Context, companyID, invoiceID uuid.UUID) (InvoiceView, error) {
	dbInv, err := s.q.GetInvoice(ctx, invoiceID)
	if err != nil {
		return InvoiceView{}, err
	}
	if dbInv.CompanyID != companyID {
		return InvoiceView{}, ErrForeignCompany
	}
	co, err := s.q.GetCompany(ctx, companyID)
	if err != nil {
		return InvoiceView{}, err
	}
	cust, err := s.GetCounterparty(ctx, companyID, dbInv.CounterpartyID)
	if err != nil {
		return InvoiceView{}, err
	}
	lineRows, err := s.q.ListInvoiceLines(ctx, invoiceID)
	if err != nil {
		return InvoiceView{}, err
	}

	view := InvoiceView{
		Number:           dbInv.Number,
		Status:           dbInv.Status,
		Date:             dbInv.InvoiceDate.Time,
		DueDate:          dbInv.DueDate.Time,
		CompanyName:      co.Name,
		CompanyOrgNr:     co.Orgnr,
		CompanyMomsRegNr: co.Momsregnr,
		CompanyAddress:   co.Address,
		CompanyPostal:    strings.TrimSpace(co.PostalCode + " " + co.City),
		CompanyFSkatt:    co.Fskatt,
		Bankgiro:         co.Bankgiro,
		IBAN:             co.Iban,
		Customer:         cust,
		Currency:         dbInv.Currency,
		OCR:              dbInv.Ocr,
	}
	inv := invoice.Invoice{Currency: dbInv.Currency, RatePPM: dbInv.RatePpm}
	sumIdx := map[moms.Code]int{} // code -> index into view.VATSummary (order preserved)
	seenNote := map[string]bool{}
	for _, r := range lineRows {
		code := moms.Code(r.VatCode)
		l := invoice.Line{
			QuantityMilli: r.QuantityMilli,
			UnitPriceOre:  ledger.Amount(r.UnitPriceOre),
			VATCode:       code,
		}
		inv.Lines = append(inv.Lines, l)
		net, vat := l.Net(), l.VAT()
		view.Lines = append(view.Lines, InvoiceViewLine{
			Description:   r.Description,
			QuantityMilli: r.QuantityMilli,
			UnitPrice:     ledger.Amount(r.UnitPriceOre),
			VATLabel:      code.LineLabel(),
			Net:           net,
			VAT:           vat,
		})
		view.Net += net
		view.VAT += vat
		// Per-rate VAT breakout (legal requirement on a faktura).
		if i, ok := sumIdx[code]; ok {
			view.VATSummary[i].Net += net
			view.VATSummary[i].VAT += vat
		} else {
			sumIdx[code] = len(view.VATSummary)
			view.VATSummary = append(view.VATSummary, VATSummaryRow{Label: code.Label(), Net: net, VAT: vat})
		}
		if note := code.LegalNote(); note != "" && !seenNote[note] {
			seenNote[note] = true
			view.LegalNotes = append(view.LegalNotes, note)
		}
	}
	view.Total = view.Net + view.VAT
	view.TotalSEK = inv.GrossSEK()
	view.AmountPaid = ledger.Amount(dbInv.AmountPaidOre)
	if dbInv.CreditsInvoiceID.Valid {
		view.IsCredit = true
		if orig, err := s.q.GetInvoice(ctx, uuidFromPg(dbInv.CreditsInvoiceID)); err == nil {
			view.CreditsNumber = orig.Number
		}
	}
	return view, nil
}

// RenderInvoicePDF loads an invoice and renders it to a PDF via Gotenberg.
func (s *Store) RenderInvoicePDF(ctx context.Context, g *render.Gotenberg, companyID, invoiceID uuid.UUID) ([]byte, error) {
	v, err := s.InvoiceForRender(ctx, companyID, invoiceID)
	if err != nil {
		return nil, err
	}
	ri := render.Invoice{
		Number:           v.Number,
		Date:             fmtDate(v.Date),
		DueDate:          fmtDate(v.DueDate),
		CompanyName:      v.CompanyName,
		CompanyOrgNr:     v.CompanyOrgNr,
		CompanyMomsRegNr: v.CompanyMomsRegNr,
		CompanyAddress:   v.CompanyAddress,
		CompanyPostal:    v.CompanyPostal,
		CompanyFSkatt:    v.CompanyFSkatt,
		Bankgiro:         v.Bankgiro,
		IBAN:             v.IBAN,
		CustomerName:     v.Customer.Name,
		CustomerOrgNr:    v.Customer.OrgNr,
		CustomerAddress:  v.Customer.Address,
		OCR:              v.OCR,
		Currency:         v.Currency,
		IsCredit:         v.IsCredit,
		CreditsNumber:    v.CreditsNumber,
		LegalNotes:       v.LegalNotes,
		NetKr:            v.Net.String(),
		VATKr:            v.VAT.String(),
		TotalKr:          v.Total.String(),
	}
	for _, l := range v.Lines {
		ri.Lines = append(ri.Lines, render.InvoiceLine{
			Description: l.Description,
			Quantity:    milliStr(l.QuantityMilli),
			UnitPrice:   l.UnitPrice.String(),
			VATLabel:    l.VATLabel,
			Net:         l.Net.String(),
		})
	}
	for _, vs := range v.VATSummary {
		ri.VATSummary = append(ri.VATSummary, render.VATRow{
			Label: vs.Label, NetKr: vs.Net.String(), VATKr: vs.VAT.String(),
		})
	}
	return render.RenderInvoicePDF(ctx, g, ri)
}

func fmtDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// milliStr formats a milli-quantity (1000 = 1.0) with up to three decimals,
// trailing zeros trimmed, Swedish decimal comma.
func milliStr(m int64) string {
	if m%1000 == 0 {
		return fmt.Sprintf("%d", m/1000)
	}
	s := fmt.Sprintf("%d,%03d", m/1000, m%1000)
	return strings.TrimRight(s, "0")
}
