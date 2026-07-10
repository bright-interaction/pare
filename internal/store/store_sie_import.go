// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	gen "github.com/bright-interaction/pare/internal/db/generated"
	"github.com/bright-interaction/pare/internal/ledger"
	"github.com/bright-interaction/pare/internal/sie"
)

// SIEImportResult summarizes what an import posted.
type SIEImportResult struct {
	AccountsSeeded int
	OpeningPosted  bool
	Vouchers       int
}

// ImportSIE loads a parsed SIE 4 file into the ledger: it seeds any accounts the
// file references (so vouchers validate), posts an opening-balance verifikat from
// the #IB rows, then replays every voucher through the immutable posting path.
// The whole import is one transaction, so a single unbalanced voucher or unknown
// account rolls the entire import back. Intended for onboarding an empty
// company; re-importing would duplicate the books.
func (s *Store) ImportSIE(ctx context.Context, companyID uuid.UUID, exp sie.Export) (SIEImportResult, error) {
	var res SIEImportResult
	err := s.inTx(ctx, func(qtx *gen.Queries) error {
		// 1. Seed the file's chart so every referenced account exists.
		for _, a := range exp.Accounts {
			if err := qtx.UpsertAccount(ctx, gen.UpsertAccountParams{
				CompanyID: companyID, Number: a.Number, Name: a.Name, Class: kontoklass(a.Number),
			}); err != nil {
				return err
			}
			res.AccountsSeeded++
		}

		// 2. Opening balances (#IB year 0) as a single verifikat. A valid SIE's
		// opening balances sum to zero, so postVerification's balance check is the
		// integrity gate.
		var ibLines []ledger.Line
		for _, b := range exp.OpeningBalances {
			switch {
			case b.Amount > 0:
				ibLines = append(ibLines, ledger.Line{Account: b.Account, Debit: ledger.Amount(b.Amount)})
			case b.Amount < 0:
				ibLines = append(ibLines, ledger.Line{Account: b.Account, Credit: ledger.Amount(-b.Amount)})
			}
		}
		if len(ibLines) > 0 {
			date := exp.YearStart
			if date.IsZero() {
				date = time.Now()
			}
			if _, err := s.postVerification(ctx, qtx, companyID, "IB", date, "Ingående balans (import)", ibLines, uuid.Nil); err != nil {
				return fmt.Errorf("store: opening balance: %w", err)
			}
			res.OpeningPosted = true
		}

		// 3. Replay vouchers. Pare assigns fresh per-series numbers; the source
		// date, series and text are preserved.
		for _, v := range exp.Vouchers {
			lines := make([]ledger.Line, 0, len(v.Lines))
			for _, l := range v.Lines {
				if l.Amount >= 0 {
					lines = append(lines, ledger.Line{Account: l.Account, Debit: ledger.Amount(l.Amount)})
				} else {
					lines = append(lines, ledger.Line{Account: l.Account, Credit: ledger.Amount(-l.Amount)})
				}
			}
			series := v.Series
			if series == "" {
				series = "A"
			}
			text := v.Text
			if text == "" {
				text = "Import"
			}
			if _, err := s.postVerification(ctx, qtx, companyID, series, v.Date, text, lines, uuid.Nil); err != nil {
				return fmt.Errorf("store: import voucher %s%d: %w", v.Series, v.Number, err)
			}
			res.Vouchers++
		}

		return s.logAudit(ctx, qtx, companyID, "import_sie", "company", companyID.String(),
			fmt.Sprintf("%d verifikat, %d konton", res.Vouchers, res.AccountsSeeded))
	})
	return res, err
}
