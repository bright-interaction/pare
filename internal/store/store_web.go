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

// ChartAccounts returns the company's chart of accounts for UI dropdowns.
func (s *Store) ChartAccounts(ctx context.Context, companyID uuid.UUID) ([]ledger.Account, error) {
	rows, err := s.q.ListAccounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, ledger.Account{Number: r.Number, Name: r.Name, DefaultVATCode: r.DefaultVatCode})
	}
	return out, nil
}

// CompanyInfo is the operator's own company: identity + the seller profile
// printed on invoices (VAT number, address, payee account, F-skatt status).
type CompanyInfo struct {
	ID         uuid.UUID
	Name       string
	OrgNr      string
	MomsRegNr  string
	Address    string
	PostalCode string
	City       string
	Bankgiro   string
	IBAN       string
	FSkatt     bool
}

// Company returns the company header + seller profile.
func (s *Store) Company(ctx context.Context, companyID uuid.UUID) (CompanyInfo, error) {
	co, err := s.q.GetCompany(ctx, companyID)
	if err != nil {
		return CompanyInfo{}, err
	}
	return CompanyInfo{
		ID: co.ID, Name: co.Name, OrgNr: co.Orgnr,
		MomsRegNr: co.Momsregnr, Address: co.Address, PostalCode: co.PostalCode,
		City: co.City, Bankgiro: co.Bankgiro, IBAN: co.Iban, FSkatt: co.Fskatt,
	}, nil
}

// UpdateCompanyProfile saves the editable seller profile (name/orgnr + invoice
// fields). Audit-logged.
func (s *Store) UpdateCompanyProfile(ctx context.Context, ci CompanyInfo) error {
	if ci.Name == "" {
		return errors.New("store: company name required")
	}
	if err := s.q.UpdateCompanyProfile(ctx, gen.UpdateCompanyProfileParams{
		ID: ci.ID, Name: ci.Name, Orgnr: ci.OrgNr, Momsregnr: ci.MomsRegNr,
		Address: ci.Address, PostalCode: ci.PostalCode, City: ci.City,
		Bankgiro: ci.Bankgiro, Iban: ci.IBAN, Fskatt: ci.FSkatt,
	}); err != nil {
		return err
	}
	return s.logAudit(ctx, s.q, ci.ID, "update_company_profile", "company", ci.ID.String(), ci.Name)
}

// ListCounterparties returns all counterparties with identities decrypted (for
// the operator UI, behind session auth; never crosses the MCP boundary).
func (s *Store) ListCounterparties(ctx context.Context, companyID uuid.UUID) ([]Counterparty, error) {
	rows, err := s.q.ListCounterparties(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Counterparty, 0, len(rows))
	for _, r := range rows {
		cp, err := s.GetCounterparty(ctx, companyID, r.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, nil
}

// ListInvoiceSummaries returns every invoice (all statuses) with customer and
// total resolved, newest first is not guaranteed; ordering follows created_at.
func (s *Store) ListInvoiceSummaries(ctx context.Context, companyID uuid.UUID) ([]InvoiceSummary, error) {
	invs, err := s.q.ListInvoices(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]InvoiceSummary, 0, len(invs))
	for _, in := range invs {
		v, err := s.InvoiceForRender(ctx, companyID, in.ID)
		if err != nil {
			return nil, err
		}
		overdue := in.Status == "finalized" && !in.CreditsInvoiceID.Valid && !v.DueDate.IsZero() && v.DueDate.Before(time.Now().Truncate(24*time.Hour))
		out = append(out, InvoiceSummary{
			ID:            in.ID,
			Number:        v.Number,
			CustomerName:  v.Customer.Name,
			CustomerOrgNr: v.Customer.OrgNr,
			Total:         v.Total,
			Currency:      v.Currency,
			TotalSEK:      v.TotalSEK,
			AmountPaid:    v.AmountPaid,
			DueDate:       fmtDate(v.DueDate),
			Status:        in.Status,
			Overdue:       overdue,
			IsCredit:      in.CreditsInvoiceID.Valid,
		})
	}
	return out, nil
}
