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

	gen "github.com/brightinteraction/pare/internal/db/generated"
	"github.com/brightinteraction/pare/internal/invoice"
	"github.com/brightinteraction/pare/internal/ledger"
	"github.com/brightinteraction/pare/internal/moms"
	"github.com/brightinteraction/pare/internal/render"
	"github.com/brightinteraction/pare/internal/sie"
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
func (s *Store) FinalizeInvoice(ctx context.Context, companyID, invoiceID uuid.UUID, number string, date, due time.Time) (uuid.UUID, error) {
	dbInv, err := s.q.GetInvoice(ctx, invoiceID)
	if err != nil {
		return uuid.Nil, err
	}
	if dbInv.CompanyID != companyID {
		return uuid.Nil, ErrForeignCompany
	}
	if dbInv.Status != "draft" {
		return uuid.Nil, ErrNotDraft
	}
	lineRows, err := s.q.ListInvoiceLines(ctx, invoiceID)
	if err != nil {
		return uuid.Nil, err
	}
	inv := invoice.Invoice{Number: number, Date: date, DueDate: due, Currency: dbInv.Currency, RatePPM: dbInv.RatePpm}
	for _, r := range lineRows {
		inv.Lines = append(inv.Lines, invoice.Line{
			Description:   r.Description,
			QuantityMilli: r.QuantityMilli,
			UnitPriceOre:  ledger.Amount(r.UnitPriceOre),
			VATCode:       moms.Code(r.VatCode),
		})
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	qtx := s.q.WithTx(tx)

	verID, err := s.postVerification(ctx, qtx, companyID, "F", date, "Faktura "+number, inv.VerificationLines(), uuid.Nil)
	if err != nil {
		return uuid.Nil, err
	}
	if err := qtx.FinalizeInvoice(ctx, gen.FinalizeInvoiceParams{
		ID:             invoiceID,
		Number:         number,
		InvoiceDate:    pgDate(date),
		DueDate:        pgDateOrNull(due),
		VerificationID: pgUUID(verID),
		CompanyID:      companyID,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("store: finalize invoice: %w", err)
	}
	if err := s.logAudit(ctx, qtx, companyID, "finalize_invoice", "invoice", invoiceID.String(), number+" "+inv.GrossSEK().String()+" SEK"); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return verID, nil
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
	if len(vers) > 0 {
		y := vers[0].Vdate.Time.Year()
		exp.YearStart = time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		exp.YearEnd = time.Date(y, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	return exp, nil
}

// InvoiceView is a fully-resolved invoice for rendering (customer decrypted,
// amounts computed). It is what the PDF template and the operator UI consume.
type InvoiceView struct {
	Number       string
	Status       string
	Date         time.Time
	DueDate      time.Time
	CompanyName  string
	CompanyOrgNr string
	Customer     Counterparty
	Lines        []InvoiceViewLine
	Net          ledger.Amount
	VAT          ledger.Amount
	Total        ledger.Amount
	Currency     string
	TotalSEK     ledger.Amount // gross booked to Kundfordringar in SEK
	OCR          string
}

// InvoiceViewLine is a rendered invoice line.
type InvoiceViewLine struct {
	Description   string
	QuantityMilli int64
	UnitPrice     ledger.Amount
	VATRate       int
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
		Number:       dbInv.Number,
		Status:       dbInv.Status,
		Date:         dbInv.InvoiceDate.Time,
		DueDate:      dbInv.DueDate.Time,
		CompanyName:  co.Name,
		CompanyOrgNr: co.Orgnr,
		Customer:     cust,
		Currency:     dbInv.Currency,
		OCR:          dbInv.Ocr,
	}
	inv := invoice.Invoice{Currency: dbInv.Currency, RatePPM: dbInv.RatePpm}
	for _, r := range lineRows {
		l := invoice.Line{
			QuantityMilli: r.QuantityMilli,
			UnitPriceOre:  ledger.Amount(r.UnitPriceOre),
			VATCode:       moms.Code(r.VatCode),
		}
		inv.Lines = append(inv.Lines, l)
		net, vat := l.Net(), l.VAT()
		view.Lines = append(view.Lines, InvoiceViewLine{
			Description:   r.Description,
			QuantityMilli: r.QuantityMilli,
			UnitPrice:     ledger.Amount(r.UnitPriceOre),
			VATRate:       int(moms.Code(r.VatCode).Rate()),
			Net:           net,
			VAT:           vat,
		})
		view.Net += net
		view.VAT += vat
	}
	view.Total = view.Net + view.VAT
	view.TotalSEK = inv.GrossSEK()
	return view, nil
}

// RenderInvoicePDF loads an invoice and renders it to a PDF via Gotenberg.
func (s *Store) RenderInvoicePDF(ctx context.Context, g *render.Gotenberg, companyID, invoiceID uuid.UUID) ([]byte, error) {
	v, err := s.InvoiceForRender(ctx, companyID, invoiceID)
	if err != nil {
		return nil, err
	}
	ri := render.Invoice{
		Number:          v.Number,
		Date:            fmtDate(v.Date),
		DueDate:         fmtDate(v.DueDate),
		CompanyName:     v.CompanyName,
		CompanyOrgNr:    v.CompanyOrgNr,
		CustomerName:    v.Customer.Name,
		CustomerOrgNr:   v.Customer.OrgNr,
		CustomerAddress: v.Customer.Address,
		OCR:             v.OCR,
		Currency:        v.Currency,
		NetKr:           v.Net.String(),
		VATKr:           v.VAT.String(),
		TotalKr:         v.Total.String(),
	}
	for _, l := range v.Lines {
		ri.Lines = append(ri.Lines, render.InvoiceLine{
			Description: l.Description,
			Quantity:    milliStr(l.QuantityMilli),
			UnitPrice:   l.UnitPrice.String(),
			VATRate:     fmt.Sprintf("%d %%", l.VATRate),
			Net:         l.Net.String(),
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
