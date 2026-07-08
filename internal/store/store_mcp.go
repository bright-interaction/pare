// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/brightinteraction/pare/internal/ledger"
)

// ErrNoCompany is returned when no company has been bootstrapped yet.
var ErrNoCompany = errors.New("store: no company")

// DefaultCompany returns the single (first) company. Pare V1 is single-company;
// multi-company selection is a pro-overlay concern.
func (s *Store) DefaultCompany(ctx context.Context) (uuid.UUID, error) {
	cos, err := s.q.ListCompanies(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if len(cos) == 0 {
		return uuid.Nil, ErrNoCompany
	}
	return cos[0].ID, nil
}

// InvoiceSummary is a finalized invoice with its customer resolved (plaintext;
// the MCP boundary tokenizes it before it reaches the LLM).
type InvoiceSummary struct {
	Number        string
	CustomerName  string
	CustomerOrgNr string
	Total         ledger.Amount
	DueDate       string
	Status        string
}

// UnpaidInvoices lists finalized invoices that are not yet paid.
func (s *Store) UnpaidInvoices(ctx context.Context, companyID uuid.UUID) ([]InvoiceSummary, error) {
	invs, err := s.q.ListInvoices(ctx, companyID)
	if err != nil {
		return nil, err
	}
	var out []InvoiceSummary
	for _, in := range invs {
		if in.Status != "finalized" {
			continue
		}
		v, err := s.InvoiceForRender(ctx, companyID, in.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, InvoiceSummary{
			Number:        v.Number,
			CustomerName:  v.Customer.Name,
			CustomerOrgNr: v.Customer.OrgNr,
			Total:         v.Total,
			DueDate:       fmtDate(v.DueDate),
			Status:        in.Status,
		})
	}
	return out, nil
}

// BalancesMap returns per-account net balances (debit-positive öre) as a map,
// for moms.Report and result computation.
func (s *Store) BalancesMap(ctx context.Context, companyID uuid.UUID) (map[string]ledger.Amount, error) {
	tb, err := s.TrialBalance(ctx, companyID)
	if err != nil {
		return nil, err
	}
	m := make(map[string]ledger.Amount, len(tb))
	for _, r := range tb {
		m[r.Account] = r.Net
	}
	return m, nil
}
