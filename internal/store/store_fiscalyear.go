// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	gen "github.com/brightinteraction/pare/internal/db/generated"
	"github.com/brightinteraction/pare/internal/ledger"
)

// closeSeries is the voucher series for year-end close (bokslut) entries. The
// resultaträkning excludes it so a closed year still shows its real P&L.
const closeSeries = "O"

var (
	// ErrYearClosed is returned when closing an already-closed fiscal year.
	ErrYearClosed = errors.New("store: fiscal year is already closed")
	// ErrNothingToClose is returned when a fiscal year has no result to close.
	ErrNothingToClose = errors.New("store: nothing to close for that fiscal year")
)

// FiscalYear is a räkenskapsår.
type FiscalYear struct {
	ID       uuid.UUID
	Label    string
	StartsOn time.Time
	EndsOn   time.Time
	Closed   bool
}

func fiscalYearFrom(r gen.FiscalYear) FiscalYear {
	return FiscalYear{ID: r.ID, Label: r.Label, StartsOn: r.StartsOn.Time, EndsOn: r.EndsOn.Time, Closed: r.ClosedAt.Valid}
}

// EnsureFiscalYear creates a calendar-year räkenskapsår if none starts on that
// year's Jan 1 (idempotent via the ON CONFLICT DO NOTHING insert).
func (s *Store) EnsureFiscalYear(ctx context.Context, companyID uuid.UUID, year int) error {
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
	_, err := s.q.InsertFiscalYear(ctx, gen.InsertFiscalYearParams{
		CompanyID: companyID, Label: strconv.Itoa(year), StartsOn: pgDate(start), EndsOn: pgDate(end),
	})
	if errors.Is(err, pgx.ErrNoRows) { // ON CONFLICT DO NOTHING returned no row
		return nil
	}
	return err
}

// ListFiscalYears returns the company's räkenskapsår, oldest first.
func (s *Store) ListFiscalYears(ctx context.Context, companyID uuid.UUID) ([]FiscalYear, error) {
	rows, err := s.q.ListFiscalYears(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]FiscalYear, len(rows))
	for i, r := range rows {
		out[i] = fiscalYearFrom(r)
	}
	return out, nil
}

// CloseFiscalYear posts the bokslut close: it zeroes the year's result accounts
// into 2099 (Årets resultat) via a series-"O" verifikat dated year-end, locks the
// period through that date, and marks the year closed. All in one transaction.
func (s *Store) CloseFiscalYear(ctx context.Context, companyID, fyID uuid.UUID) (uuid.UUID, error) {
	fy, err := s.q.GetFiscalYear(ctx, fyID)
	if err != nil {
		return uuid.Nil, err
	}
	if fy.CompanyID != companyID {
		return uuid.Nil, ErrForeignCompany
	}
	if fy.ClosedAt.Valid {
		return uuid.Nil, ErrYearClosed
	}
	start, end := fy.StartsOn.Time, fy.EndsOn.Time

	// The real P&L for the year (excluding any prior close entries).
	resTB, err := s.TrialBalanceBetweenExclSeries(ctx, companyID, start, end, closeSeries)
	if err != nil {
		return uuid.Nil, err
	}
	var lines []ledger.Line
	for _, r := range resTB {
		if !r.Class.IsResult() || r.Net == 0 {
			continue
		}
		if r.Net > 0 { // debit-balance account (e.g. expense): credit to zero it
			lines = append(lines, ledger.Line{Account: r.Account, Credit: r.Net})
		} else {
			lines = append(lines, ledger.Line{Account: r.Account, Debit: -r.Net})
		}
	}
	if len(lines) == 0 {
		return uuid.Nil, ErrNothingToClose
	}
	result := ledger.ResultOf(resTB) // profit positive
	if result > 0 {
		lines = append(lines, ledger.Line{Account: "2099", Credit: result})
	} else {
		lines = append(lines, ledger.Line{Account: "2099", Debit: -result})
	}

	var verID uuid.UUID
	err = s.inTx(ctx, func(qtx *gen.Queries) error {
		// Post the close BEFORE locking, else the period lock would reject its own
		// year-end-dated verifikat.
		id, err := s.postVerification(ctx, qtx, companyID, closeSeries, end, "Bokslut "+fy.Label, lines, uuid.Nil)
		if err != nil {
			return err
		}
		verID = id
		n, err := qtx.CloseFiscalYear(ctx, gen.CloseFiscalYearParams{ID: fyID, CompanyID: companyID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrYearClosed
		}
		if err := qtx.SetLockedThrough(ctx, gen.SetLockedThroughParams{ID: companyID, LockedThrough: pgDate(end)}); err != nil {
			return err
		}
		return s.logAudit(ctx, qtx, companyID, "close_fiscal_year", "fiscal_year", fyID.String(), fmt.Sprintf("bokslut %s, resultat %s", fy.Label, result))
	})
	return verID, err
}
