// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package store

import (
	"context"

	"github.com/google/uuid"

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

// CompanyInfo is the operator's own company (name + org number).
type CompanyInfo struct {
	ID    uuid.UUID
	Name  string
	OrgNr string
}

// Company returns the company header.
func (s *Store) Company(ctx context.Context, companyID uuid.UUID) (CompanyInfo, error) {
	co, err := s.q.GetCompany(ctx, companyID)
	if err != nil {
		return CompanyInfo{}, err
	}
	return CompanyInfo{ID: co.ID, Name: co.Name, OrgNr: co.Orgnr}, nil
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
		out = append(out, InvoiceSummary{
			ID:            in.ID,
			Number:        v.Number,
			CustomerName:  v.Customer.Name,
			CustomerOrgNr: v.Customer.OrgNr,
			Total:         v.Total,
			Currency:      v.Currency,
			TotalSEK:      v.TotalSEK,
			DueDate:       fmtDate(v.DueDate),
			Status:        in.Status,
		})
	}
	return out, nil
}
