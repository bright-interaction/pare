// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

package ledger

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrUnbalanced means total debit does not equal total credit.
	ErrUnbalanced = errors.New("ledger: verification does not balance (debit != credit)")
	// ErrTooFewLines means a verification has fewer than two lines.
	ErrTooFewLines = errors.New("ledger: verification needs at least two lines")
	// ErrLineSide means a line has both or neither of debit/credit set.
	ErrLineSide = errors.New("ledger: each line must have exactly one of debit or credit")
	// ErrNoAccount means a line is missing its account number.
	ErrNoAccount = errors.New("ledger: line has no account")
	// ErrNegative means an amount is negative.
	ErrNegative = errors.New("ledger: amounts must be non-negative")
)

// Line is one posting row: a debit or a credit against a BAS account.
type Line struct {
	Account string // BAS account number, e.g. "1930"
	Debit   Amount
	Credit  Amount
	VATCode string // moms code, optional
}

// Verification is a verifikat: an immutable, balanced set of postings for one
// business event. Once posted it is never edited; corrections are made by
// Reverse (a rättelseverifikat), per Bokföringslagen.
type Verification struct {
	Series      string // e.g. "A"
	Number      int
	Date        time.Time
	Description string
	Lines       []Line
	ReversalOf  string // ID of the verification this one corrects, if any
}

// ID is the human reference for the verification, e.g. "A14".
func (v Verification) ID() string {
	return fmt.Sprintf("%s%d", v.Series, v.Number)
}

// Totals returns the summed debit and credit across all lines.
func (v Verification) Totals() (debit, credit Amount) {
	for _, l := range v.Lines {
		debit += l.Debit
		credit += l.Credit
	}
	return
}

// Validate enforces the double-entry invariants. A verification that fails
// Validate must never be posted.
func (v Verification) Validate() error {
	if len(v.Lines) < 2 {
		return ErrTooFewLines
	}
	var d, c Amount
	for _, l := range v.Lines {
		if l.Account == "" {
			return ErrNoAccount
		}
		if l.Debit < 0 || l.Credit < 0 {
			return ErrNegative
		}
		if (l.Debit == 0) == (l.Credit == 0) {
			return ErrLineSide
		}
		d += l.Debit
		c += l.Credit
	}
	if d != c {
		return ErrUnbalanced
	}
	return nil
}

// Reverse builds a correcting entry that swaps debit and credit on every line.
func Reverse(orig Verification, series string, number int, date time.Time, reason string) Verification {
	lines := make([]Line, len(orig.Lines))
	for i, l := range orig.Lines {
		lines[i] = Line{Account: l.Account, Debit: l.Credit, Credit: l.Debit, VATCode: l.VATCode}
	}
	if reason == "" {
		reason = "Rättelse av " + orig.ID()
	}
	return Verification{
		Series:      series,
		Number:      number,
		Date:        date,
		Description: reason,
		Lines:       lines,
		ReversalOf:  orig.ID(),
	}
}
