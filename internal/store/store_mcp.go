// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/bright-interaction/pare/internal/ledger"
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

// InvoiceSummary is an invoice with its customer resolved (plaintext; the MCP
// boundary tokenizes it before it reaches the LLM).
type InvoiceSummary struct {
	ID            uuid.UUID
	Number        string
	CustomerName  string
	CustomerOrgNr string
	Total         ledger.Amount // in the invoice currency
	Currency      string
	TotalSEK      ledger.Amount // booked to Kundfordringar in SEK
	AmountPaid    ledger.Amount // SEK settled so far (partial payments)
	DueDate       string
	Status        string
	Overdue       bool // finalized + past due date
	IsCredit      bool // this row is a kreditfaktura (negative document)
	Matched       bool // transient: matches a reconciliation amount (not persisted)
}

// PartlyPaid reports whether the invoice has been partially (not fully) settled.
func (s InvoiceSummary) PartlyPaid() bool {
	return s.Status == "finalized" && s.AmountPaid > 0 && s.AmountPaid < s.TotalSEK
}

// UnpaidInvoices lists finalized invoices that are not yet paid.
func (s *Store) UnpaidInvoices(ctx context.Context, companyID uuid.UUID) ([]InvoiceSummary, error) {
	invs, err := s.q.ListInvoices(ctx, companyID)
	if err != nil {
		return nil, err
	}
	var out []InvoiceSummary
	for _, in := range invs {
		// Only open receivables: finalized customer invoices, not credit notes.
		if in.Status != "finalized" || in.CreditsInvoiceID.Valid {
			continue
		}
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

// MatchOpenInvoices returns open (finalized, non-credit) customer invoices whose
// outstanding balance matches an incoming payment amount (SEK öre), best matches
// first (exact, then within the öre tolerance). This is the smart reconciliation
// helper: a bank payment lands, find the invoice it settles.
func (s *Store) MatchOpenInvoices(ctx context.Context, companyID uuid.UUID, amount ledger.Amount) ([]InvoiceSummary, error) {
	all, err := s.ListInvoiceSummaries(ctx, companyID)
	if err != nil {
		return nil, err
	}
	var exact, near []InvoiceSummary
	for _, inv := range all {
		if inv.Status != "finalized" || inv.IsCredit {
			continue
		}
		diff := (inv.TotalSEK - inv.AmountPaid) - amount
		switch {
		case diff == 0:
			exact = append(exact, inv)
		case absAmount(diff) < oresRoundingThreshold:
			near = append(near, inv)
		}
	}
	return append(exact, near...), nil
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
