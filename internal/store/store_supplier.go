// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	gen "github.com/bright-interaction/pare/internal/db/generated"
	"github.com/bright-interaction/pare/internal/ledger"
	"github.com/bright-interaction/pare/internal/moms"
)

// SupplierInvoiceView is a resolved supplier invoice (supplier decrypted, amounts
// computed) for the operator UI.
type SupplierInvoiceView struct {
	ID             uuid.UUID
	SupplierName   string
	SupplierOrgNr  string
	SupplierNumber string
	Date           time.Time
	DueDate        time.Time
	CostAccount    string
	Net            ledger.Amount
	VATCode        moms.PurchaseCode
	VATLabel       string
	Description    string
	Status         string
	Payable        ledger.Amount // amount owed to the supplier (gross)
}

func supplierView(s *Store, ctx context.Context, companyID uuid.UUID, row gen.SupplierInvoice) (SupplierInvoiceView, error) {
	cp, err := s.GetCounterparty(ctx, companyID, row.CounterpartyID)
	if err != nil {
		return SupplierInvoiceView{}, err
	}
	code := moms.PurchaseCode(row.VatCode)
	return SupplierInvoiceView{
		ID: row.ID, SupplierName: cp.Name, SupplierOrgNr: cp.OrgNr,
		SupplierNumber: row.SupplierNumber, Date: row.InvoiceDate.Time, DueDate: row.DueDate.Time,
		CostAccount: row.CostAccount, Net: ledger.Amount(row.NetOre), VATCode: code,
		VATLabel: code.Label(), Description: row.Description, Status: row.Status,
		Payable: moms.Payable(ledger.Amount(row.NetOre), code),
	}, nil
}

// CreateSupplierInvoice inserts a draft leverantorsfaktura.
func (s *Store) CreateSupplierInvoice(ctx context.Context, companyID, counterpartyID uuid.UUID, supplierNumber string, date, due time.Time, costAccount string, net ledger.Amount, code moms.PurchaseCode, description string) (uuid.UUID, error) {
	if !moms.ValidPurchaseCode(string(code)) {
		return uuid.Nil, errors.New("store: unknown purchase VAT code")
	}
	if net <= 0 {
		return uuid.Nil, errors.New("store: net amount must be positive")
	}
	cp, err := s.q.GetCounterparty(ctx, counterpartyID)
	if err != nil {
		return uuid.Nil, err
	}
	if cp.CompanyID != companyID {
		return uuid.Nil, ErrForeignCompany
	}
	row, err := s.q.InsertSupplierInvoice(ctx, gen.InsertSupplierInvoiceParams{
		CompanyID: companyID, CounterpartyID: counterpartyID, SupplierNumber: supplierNumber,
		InvoiceDate: pgDateOrNull(date), DueDate: pgDateOrNull(due),
		CostAccount: costAccount, NetOre: int64(net), VatCode: string(code), Description: description,
	})
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.logAudit(ctx, s.q, companyID, "create_supplier_invoice", "supplier_invoice", row.ID.String(), string(code)); err != nil {
		return uuid.Nil, err
	}
	return row.ID, nil
}

// FinalizeSupplierInvoice posts the purchase verifikat (series L, self-assessing
// forvarvsmoms for a foreign service) and marks the invoice finalized.
func (s *Store) FinalizeSupplierInvoice(ctx context.Context, companyID, id uuid.UUID) (uuid.UUID, error) {
	row, err := s.q.GetSupplierInvoice(ctx, id)
	if err != nil {
		return uuid.Nil, err
	}
	if row.CompanyID != companyID {
		return uuid.Nil, ErrForeignCompany
	}
	if row.Status != "draft" {
		return uuid.Nil, ErrNotDraft
	}
	lines := moms.PurchaseLines(row.CostAccount, ledger.Amount(row.NetOre), moms.PurchaseCode(row.VatCode))
	// The verifikat description carries no supplier identity (identities stay
	// encrypted on the counterparty), only the supplier's own invoice number.
	desc := "Leverantörsfaktura"
	if row.SupplierNumber != "" {
		desc += " " + row.SupplierNumber
	}
	var verID uuid.UUID
	err = s.inTx(ctx, func(qtx *gen.Queries) error {
		vid, err := s.postVerification(ctx, qtx, companyID, "L", row.InvoiceDate.Time, desc, lines, uuid.Nil)
		if err != nil {
			return err
		}
		verID = vid
		n, err := qtx.FinalizeSupplierInvoice(ctx, gen.FinalizeSupplierInvoiceParams{ID: id, VerificationID: pgUUID(vid), CompanyID: companyID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotDraft
		}
		return s.logAudit(ctx, qtx, companyID, "finalize_supplier_invoice", "supplier_invoice", id.String(), row.VatCode)
	})
	return verID, err
}

// RecordSupplierPayment settles a finalized supplier invoice: debit 2440
// Leverantorsskulder, credit the bank account. Supplier invoices are booked in
// SEK, so the payment must equal the amount owed.
func (s *Store) RecordSupplierPayment(ctx context.Context, companyID, id uuid.UUID, date time.Time, account string, amountSEK ledger.Amount) (uuid.UUID, error) {
	row, err := s.q.GetSupplierInvoice(ctx, id)
	if err != nil {
		return uuid.Nil, err
	}
	if row.CompanyID != companyID {
		return uuid.Nil, ErrForeignCompany
	}
	if row.Status != "finalized" {
		return uuid.Nil, ErrNotFinalized
	}
	gross := moms.Payable(ledger.Amount(row.NetOre), moms.PurchaseCode(row.VatCode))
	if amountSEK <= 0 {
		return uuid.Nil, ErrPaymentMismatch
	}
	// Clear the whole 2440 liability; the bank moves the amount actually paid, and
	// a small difference (paying a rounded amount) closes to 3740. A larger
	// mismatch is a data-entry error (partial supplier payments not supported).
	lines := []ledger.Line{
		{Account: "2440", Debit: gross},
		{Account: account, Credit: amountSEK},
	}
	if diff := gross - amountSEK; diff != 0 {
		if absAmount(diff) >= oresRoundingThreshold {
			return uuid.Nil, ErrPaymentMismatch
		}
		if diff > 0 { // paid slightly less: tiny rounding income
			lines = append(lines, ledger.Line{Account: "3740", Credit: diff})
		} else { // paid slightly more: tiny rounding cost
			lines = append(lines, ledger.Line{Account: "3740", Debit: -diff})
		}
	}
	desc := "Betalning leverantörsfaktura"
	if row.SupplierNumber != "" {
		desc += " " + row.SupplierNumber
	}
	var verID uuid.UUID
	err = s.inTx(ctx, func(qtx *gen.Queries) error {
		vid, err := s.postVerification(ctx, qtx, companyID, "LB", date, desc, lines, uuid.Nil)
		if err != nil {
			return err
		}
		verID = vid
		n, err := qtx.MarkSupplierInvoicePaid(ctx, gen.MarkSupplierInvoicePaidParams{
			ID: id, PaidAt: pgDate(date), PaymentVerificationID: pgUUID(vid), CompanyID: companyID,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFinalized
		}
		return s.logAudit(ctx, qtx, companyID, "record_supplier_payment", "supplier_invoice", id.String(), gross.String()+" SEK")
	})
	return verID, err
}

// SupplierInvoiceForView resolves one supplier invoice.
func (s *Store) SupplierInvoiceForView(ctx context.Context, companyID, id uuid.UUID) (SupplierInvoiceView, error) {
	row, err := s.q.GetSupplierInvoice(ctx, id)
	if err != nil {
		return SupplierInvoiceView{}, err
	}
	if row.CompanyID != companyID {
		return SupplierInvoiceView{}, ErrForeignCompany
	}
	return supplierView(s, ctx, companyID, row)
}

// ListSupplierInvoiceViews returns all supplier invoices, newest first.
func (s *Store) ListSupplierInvoiceViews(ctx context.Context, companyID uuid.UUID) ([]SupplierInvoiceView, error) {
	rows, err := s.q.ListSupplierInvoices(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]SupplierInvoiceView, 0, len(rows))
	for _, r := range rows {
		v, err := supplierView(s, ctx, companyID, r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
