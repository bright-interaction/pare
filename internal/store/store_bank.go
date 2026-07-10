// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/brightinteraction/pare/internal/bank"
	gen "github.com/brightinteraction/pare/internal/db/generated"
	"github.com/brightinteraction/pare/internal/ledger"
)

// ErrTxnNotOpen is returned when booking/ignoring a transaction that is not open.
var ErrTxnNotOpen = errors.New("store: bank transaction is not open")

// bankSeries is the voucher series for bank-reconciliation postings.
const bankSeries = "BK"

// BankTxnView is a resolved bank transaction (text decrypted, match suggested).
type BankTxnView struct {
	ID            uuid.UUID
	Date          time.Time
	Amount        ledger.Amount // signed: credit + / debit -
	Text          string
	Ref           string
	BankAccount   string
	Status        string
	IsCredit      bool
	MatchNumber   string    // suggested invoice (single exact match on a credit)
	MatchID       uuid.UUID // suggested invoice id
	MatchCustomer string
}

// ImportBankStatement stores parsed entries (encrypting the free text), skipping
// duplicates via a plaintext fingerprint. Returns how many were newly imported.
func (s *Store) ImportBankStatement(ctx context.Context, companyID uuid.UUID, bankAccount string, entries []bank.Entry) (int, error) {
	if bankAccount == "" {
		bankAccount = "1930"
	}
	dek, err := s.companyDEK(ctx, companyID)
	if err != nil {
		return 0, err
	}
	before, _ := s.q.ListBankTxns(ctx, companyID)
	for _, e := range entries {
		if e.Date.IsZero() {
			continue
		}
		fp := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%s", e.Date.Format("2006-01-02"), e.AmountOre, e.Text, e.Ref)))
		textEnc := ""
		if e.Text != "" {
			if textEnc, err = dek.EncryptField([]byte(e.Text)); err != nil {
				return 0, err
			}
		}
		if err := s.q.InsertBankTxn(ctx, gen.InsertBankTxnParams{
			CompanyID: companyID, TxnDate: pgDate(e.Date), AmountOre: e.AmountOre,
			TextEnc: textEnc, Ref: e.Ref, BankAccount: bankAccount, Fingerprint: hex.EncodeToString(fp[:]),
		}); err != nil {
			return 0, err
		}
	}
	after, _ := s.q.ListBankTxns(ctx, companyID)
	imported := len(after) - len(before)
	if imported > 0 {
		_ = s.logAudit(ctx, s.q, companyID, "import_bank_statement", "bank", companyID.String(), fmt.Sprintf("%d nya transaktioner", imported))
	}
	return imported, nil
}

// ListBankTransactions returns transactions with text decrypted and, for an open
// credit, a suggested invoice (a single exact outstanding match).
func (s *Store) ListBankTransactions(ctx context.Context, companyID uuid.UUID) ([]BankTxnView, error) {
	rows, err := s.q.ListBankTxns(ctx, companyID)
	if err != nil {
		return nil, err
	}
	dek, err := s.companyDEK(ctx, companyID)
	if err != nil {
		return nil, err
	}
	// Open invoices once, for match suggestions.
	invs, _ := s.ListInvoiceSummaries(ctx, companyID)
	out := make([]BankTxnView, 0, len(rows))
	for _, r := range rows {
		text := ""
		if r.TextEnc != "" {
			if b, err := dek.DecryptField(r.TextEnc); err == nil {
				text = string(b)
			}
		}
		v := BankTxnView{
			ID: r.ID, Date: r.TxnDate.Time, Amount: ledger.Amount(r.AmountOre), Text: text, Ref: r.Ref,
			BankAccount: r.BankAccount, Status: r.Status, IsCredit: r.AmountOre > 0,
		}
		if r.Status == "unmatched" && r.AmountOre > 0 {
			var matches []InvoiceSummary
			for _, inv := range invs {
				if inv.Status == "finalized" && !inv.IsCredit && inv.TotalSEK-inv.AmountPaid == ledger.Amount(r.AmountOre) {
					matches = append(matches, inv)
				}
			}
			if len(matches) == 1 {
				v.MatchNumber, v.MatchID, v.MatchCustomer = matches[0].Number, matches[0].ID, matches[0].CustomerName
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// BookBankTxnToInvoice settles a credit transaction against an invoice (records a
// payment to the transaction's bank account) and marks it booked.
func (s *Store) BookBankTxnToInvoice(ctx context.Context, companyID, txnID, invoiceID uuid.UUID) error {
	txn, err := s.q.GetBankTxn(ctx, txnID)
	if err != nil {
		return err
	}
	if txn.CompanyID != companyID {
		return ErrForeignCompany
	}
	if txn.Status != "unmatched" || txn.AmountOre <= 0 {
		return ErrTxnNotOpen
	}
	verID, err := s.RecordPayment(ctx, companyID, invoiceID, txn.TxnDate.Time, txn.BankAccount, ledger.Amount(txn.AmountOre))
	if err != nil {
		return err
	}
	_, err = s.q.MarkBankTxnBooked(ctx, gen.MarkBankTxnBookedParams{ID: txnID, CompanyID: companyID, VerificationID: pgUUID(verID), MatchedInvoiceID: pgUUID(invoiceID)})
	return err
}

// BookBankTxnToAccount books a transaction against a chosen account (money in ->
// debit bank, credit account; money out -> credit bank, debit account) and marks
// it booked. Used for costs and receipts that are not an invoice payment.
func (s *Store) BookBankTxnToAccount(ctx context.Context, companyID, txnID uuid.UUID, account string) error {
	txn, err := s.q.GetBankTxn(ctx, txnID)
	if err != nil {
		return err
	}
	if txn.CompanyID != companyID {
		return ErrForeignCompany
	}
	if txn.Status != "unmatched" {
		return ErrTxnNotOpen
	}
	amt := ledger.Amount(txn.AmountOre)
	var lines []ledger.Line
	if amt > 0 { // money in
		lines = []ledger.Line{{Account: txn.BankAccount, Debit: amt}, {Account: account, Credit: amt}}
	} else { // money out
		lines = []ledger.Line{{Account: account, Debit: -amt}, {Account: txn.BankAccount, Credit: -amt}}
	}
	desc := "Banktransaktion"
	if txn.Ref != "" {
		desc += " " + txn.Ref
	}
	err = s.inTx(ctx, func(qtx *gen.Queries) error {
		id, err := s.postVerification(ctx, qtx, companyID, bankSeries, txn.TxnDate.Time, desc, lines, uuid.Nil)
		if err != nil {
			return err
		}
		n, err := qtx.MarkBankTxnBooked(ctx, gen.MarkBankTxnBookedParams{ID: txnID, CompanyID: companyID, VerificationID: pgUUID(id), MatchedInvoiceID: pgUUID(uuid.Nil)})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrTxnNotOpen
		}
		return nil
	})
	return err
}

// IgnoreBankTxn marks a transaction ignored (e.g. an internal transfer).
func (s *Store) IgnoreBankTxn(ctx context.Context, companyID, txnID uuid.UUID) error {
	n, err := s.q.MarkBankTxnIgnored(ctx, gen.MarkBankTxnIgnoredParams{ID: txnID, CompanyID: companyID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTxnNotOpen
	}
	return nil
}
