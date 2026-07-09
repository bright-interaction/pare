// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package store persists the ledger. It wraps the sqlc-generated queries and
// applies the two invariants that must never be bypassed: double-entry balance
// (verified before any verification is written) and at-rest encryption of
// counterparty identities (via the crypto envelope layer).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brightinteraction/pare/internal/crypto"
	gen "github.com/brightinteraction/pare/internal/db/generated"
	"github.com/brightinteraction/pare/internal/ledger"
)

// Store is the ledger persistence layer.
type Store struct {
	pool     *pgxpool.Pool
	q        *gen.Queries
	kek      *crypto.KEK
	auditKey []byte // HMAC key for the audit hash chain (derived, never stored)
}

// New builds a Store over a pgx pool and the key-encryption key.
func New(pool *pgxpool.Pool, kek *crypto.KEK) *Store {
	return &Store{pool: pool, q: gen.New(pool), kek: kek, auditKey: kek.DeriveKey("pare/audit/hmac/v1")}
}

// ErrUnknownAccount is returned when a posting references an account not in the
// company's chart (the AI cannot invent accounts).
var ErrUnknownAccount = errors.New("store: account not in the chart of accounts")

// inTx runs fn inside a transaction, committing on success.
func (s *Store) inTx(ctx context.Context, fn func(*gen.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// BootstrapCompany creates a company with a fresh wrapped DEK and seeds the
// core chart of accounts. Returns the new company id.
func (s *Store) BootstrapCompany(ctx context.Context, name, orgnr string) (uuid.UUID, error) {
	dek, err := crypto.NewDEK()
	if err != nil {
		return uuid.Nil, err
	}
	wrapped, err := s.kek.WrapDEK(dek)
	if err != nil {
		return uuid.Nil, err
	}
	co, err := s.q.InsertCompany(ctx, gen.InsertCompanyParams{Name: name, Orgnr: orgnr, DekWrapped: wrapped, KeyID: s.kek.Fingerprint()})
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: insert company: %w", err)
	}
	for _, a := range ledger.CoreChart {
		if err := s.q.UpsertAccount(ctx, gen.UpsertAccountParams{
			CompanyID:      co.ID,
			Number:         a.Number,
			Name:           a.Name,
			Class:          kontoklass(a.Number),
			DefaultVatCode: a.DefaultVATCode,
		}); err != nil {
			return uuid.Nil, fmt.Errorf("store: seed account %s: %w", a.Number, err)
		}
	}
	return co.ID, nil
}

// SyncChart upserts the CoreChart into every existing company, backfilling
// accounts added to the chart after a company was bootstrapped (e.g. the
// currency-difference accounts 3960/7960 needed for foreign-currency payments)
// and refreshing renamed accounts. Idempotent; run once at startup so the
// operator never has to touch the DB when the chart grows.
func (s *Store) SyncChart(ctx context.Context) error {
	companies, err := s.q.ListCompanies(ctx)
	if err != nil {
		return err
	}
	for _, co := range companies {
		for _, a := range ledger.CoreChart {
			if err := s.q.UpsertAccount(ctx, gen.UpsertAccountParams{
				CompanyID:      co.ID,
				Number:         a.Number,
				Name:           a.Name,
				Class:          kontoklass(a.Number),
				DefaultVatCode: a.DefaultVATCode,
			}); err != nil {
				return fmt.Errorf("store: sync account %s for %s: %w", a.Number, co.ID, err)
			}
		}
	}
	return nil
}

func (s *Store) companyDEK(ctx context.Context, companyID uuid.UUID) (*crypto.DEK, error) {
	co, err := s.q.GetCompany(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("store: get company: %w", err)
	}
	raw, err := s.kek.UnwrapDEK(co.DekWrapped)
	if err != nil {
		return nil, fmt.Errorf("store: unwrap dek: %w", err)
	}
	return crypto.NewDEKFrom(raw)
}

// PostVerification validates and writes a balanced verification and its lines in
// one transaction, assigning the next number in the series. reversalOf may be
// uuid.Nil. The row is written already posted, so it is immutable afterward.
func (s *Store) PostVerification(ctx context.Context, companyID uuid.UUID, series string, date time.Time, description string, lines []ledger.Line, reversalOf uuid.UUID) (uuid.UUID, error) {
	var total ledger.Amount
	for _, l := range lines {
		total += l.Debit
	}
	var id uuid.UUID
	err := s.inTx(ctx, func(qtx *gen.Queries) error {
		var e error
		id, e = s.postVerification(ctx, qtx, companyID, series, date, description, lines, reversalOf)
		if e != nil {
			return e
		}
		return s.logAudit(ctx, qtx, companyID, "post_verification", "verification", id.String(), series+" "+total.String())
	})
	return id, err
}

// postVerification is the transactional core shared by PostVerification and
// invoice finalization; qtx must already be bound to an open transaction.
func (s *Store) postVerification(ctx context.Context, qtx *gen.Queries, companyID uuid.UUID, series string, date time.Time, description string, lines []ledger.Line, reversalOf uuid.UUID) (uuid.UUID, error) {
	v := ledger.Verification{Series: series, Date: date, Description: description, Lines: lines}
	if err := v.Validate(); err != nil {
		return uuid.Nil, err
	}
	co, err := qtx.GetCompany(ctx, companyID)
	if err != nil {
		return uuid.Nil, err
	}
	if co.LockedThrough.Valid && !date.After(co.LockedThrough.Time) {
		return uuid.Nil, ErrPeriodClosed
	}
	accts, err := qtx.ListAccounts(ctx, companyID)
	if err != nil {
		return uuid.Nil, err
	}
	known := make(map[string]bool, len(accts))
	for _, a := range accts {
		known[a.Number] = true
	}
	for _, l := range lines {
		if !known[l.Account] {
			return uuid.Nil, fmt.Errorf("%w: %s", ErrUnknownAccount, l.Account)
		}
	}
	num, err := qtx.NextVerificationNumber(ctx, gen.NextVerificationNumberParams{CompanyID: companyID, Series: series})
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: next number: %w", err)
	}
	ver, err := qtx.InsertVerification(ctx, gen.InsertVerificationParams{
		CompanyID:   companyID,
		Series:      series,
		Number:      num,
		Vdate:       pgDate(date),
		Description: description,
		ReversalOf:  pgUUID(reversalOf),
		PostedAt:    pgNow(),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: insert verification: %w", err)
	}
	for _, l := range lines {
		if err := qtx.InsertVerificationLine(ctx, gen.InsertVerificationLineParams{
			VerificationID: ver.ID,
			Account:        l.Account,
			DebitOre:       int64(l.Debit),
			CreditOre:      int64(l.Credit),
			VatCode:        l.VATCode,
		}); err != nil {
			return uuid.Nil, fmt.Errorf("store: insert line: %w", err)
		}
	}
	return ver.ID, nil
}

// TrialBalance returns per-account net balances for the company.
func (s *Store) TrialBalance(ctx context.Context, companyID uuid.UUID) ([]ledger.AccountBalance, error) {
	rows, err := s.q.TrialBalance(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.AccountBalance, len(rows))
	for i, r := range rows {
		out[i] = ledger.AccountBalance{
			Account: r.Account,
			Class:   ledger.Classify(r.Account),
			Net:     ledger.Amount(r.NetOre),
		}
	}
	return out, nil
}

// TrialBalanceBetween returns per-account net balances for verifikat dated in
// [from, to]. Used for period statements (resultaträkning) and the momsrapport.
func (s *Store) TrialBalanceBetween(ctx context.Context, companyID uuid.UUID, from, to time.Time) ([]ledger.AccountBalance, error) {
	rows, err := s.q.TrialBalanceBetween(ctx, gen.TrialBalanceBetweenParams{CompanyID: companyID, Vdate: pgDate(from), Vdate_2: pgDate(to)})
	if err != nil {
		return nil, err
	}
	out := make([]ledger.AccountBalance, len(rows))
	for i, r := range rows {
		out[i] = ledger.AccountBalance{Account: r.Account, Class: ledger.Classify(r.Account), Net: ledger.Amount(r.NetOre)}
	}
	return out, nil
}

// TrialBalanceAsOf returns cumulative per-account net balances for verifikat
// dated on or before `to`. Used for the balansräkning (a point-in-time snapshot).
func (s *Store) TrialBalanceAsOf(ctx context.Context, companyID uuid.UUID, to time.Time) ([]ledger.AccountBalance, error) {
	rows, err := s.q.TrialBalanceAsOf(ctx, gen.TrialBalanceAsOfParams{CompanyID: companyID, Vdate: pgDate(to)})
	if err != nil {
		return nil, err
	}
	out := make([]ledger.AccountBalance, len(rows))
	for i, r := range rows {
		out[i] = ledger.AccountBalance{Account: r.Account, Class: ledger.Classify(r.Account), Net: ledger.Amount(r.NetOre)}
	}
	return out, nil
}

// Counterparty is the decrypted form of a customer or supplier.
type Counterparty struct {
	ID           uuid.UUID
	Kind         string // "customer" or "supplier"
	Name         string
	OrgNr        string
	Personnummer string
	Address      string
	IBAN         string
	Erased       bool // GDPR-erased: identity fields are tombstoned
}

// ErrForeignCompany guards against reading another company's counterparty.
var ErrForeignCompany = errors.New("store: counterparty belongs to another company")

// CreateCounterparty encrypts identity fields with the company DEK and inserts.
func (s *Store) CreateCounterparty(ctx context.Context, companyID uuid.UUID, cp Counterparty) (uuid.UUID, error) {
	if cp.Name == "" {
		return uuid.Nil, errors.New("store: counterparty name required")
	}
	dek, err := s.companyDEK(ctx, companyID)
	if err != nil {
		return uuid.Nil, err
	}
	enc := func(v string) (string, error) {
		if v == "" {
			return "", nil
		}
		return dek.EncryptField([]byte(v))
	}
	fields := make([]string, 5)
	for i, v := range []string{cp.Name, cp.OrgNr, cp.Personnummer, cp.Address, cp.IBAN} {
		e, err := enc(v)
		if err != nil {
			return uuid.Nil, err
		}
		fields[i] = e
	}
	row, err := s.q.InsertCounterparty(ctx, gen.InsertCounterpartyParams{
		CompanyID:       companyID,
		Kind:            cp.Kind,
		NameEnc:         fields[0],
		OrgnrEnc:        fields[1],
		PersonnummerEnc: fields[2],
		AddressEnc:      fields[3],
		IbanEnc:         fields[4],
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: insert counterparty: %w", err)
	}
	if err := s.logAudit(ctx, s.q, companyID, "create_counterparty", "counterparty", row.ID.String(), cp.Kind); err != nil {
		return uuid.Nil, err
	}
	return row.ID, nil
}

// GetCounterparty loads and decrypts a counterparty, enforcing company scope.
func (s *Store) GetCounterparty(ctx context.Context, companyID, id uuid.UUID) (Counterparty, error) {
	row, err := s.q.GetCounterparty(ctx, id)
	if err != nil {
		return Counterparty{}, err
	}
	if row.CompanyID != companyID {
		return Counterparty{}, ErrForeignCompany
	}
	dek, err := s.companyDEK(ctx, companyID)
	if err != nil {
		return Counterparty{}, err
	}
	dec := func(v string) (string, error) {
		if v == "" {
			return "", nil
		}
		b, err := dek.DecryptField(v)
		return string(b), err
	}
	out := Counterparty{ID: row.ID, Kind: row.Kind, Erased: row.ErasedAt.Valid}
	for _, f := range []struct {
		enc string
		dst *string
	}{
		{row.NameEnc, &out.Name},
		{row.OrgnrEnc, &out.OrgNr},
		{row.PersonnummerEnc, &out.Personnummer},
		{row.AddressEnc, &out.Address},
		{row.IbanEnc, &out.IBAN},
	} {
		v, err := dec(f.enc)
		if err != nil {
			return Counterparty{}, err
		}
		*f.dst = v
	}
	return out, nil
}

// kontoklass returns the BAS account class (1..8) from the first digit.
func kontoklass(number string) int16 {
	if number == "" {
		return 0
	}
	return int16(number[0] - '0')
}

func pgDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}

func pgDateOrNull(t time.Time) pgtype.Date {
	if t.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}

func pgNow() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

func uuidFromPg(v pgtype.UUID) uuid.UUID {
	if !v.Valid {
		return uuid.Nil
	}
	return uuid.UUID(v.Bytes)
}
