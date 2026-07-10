// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"errors"

	"github.com/google/uuid"

	gen "github.com/bright-interaction/pare/internal/db/generated"
)

// erasureTombstone is what a GDPR-erased counterparty's name decrypts to. It is
// still encrypted at rest (like any name) so the column type and read path are
// unchanged; it just carries no identity.
const erasureTombstone = "[raderad enligt GDPR]"

// ErrRetentionBlocked is returned when erasure is refused because the
// counterparty appears on retained accounting records (bokföringslagen wins).
var ErrRetentionBlocked = errors.New("store: counterparty is on retained invoices and cannot be erased yet")

// ErrCounterpartyErased is returned when editing an already-erased counterparty.
var ErrCounterpartyErased = errors.New("store: counterparty is erased")

// UpdateCounterparty re-encrypts and updates a counterparty's identity fields.
// It refuses to touch an erased row.
func (s *Store) UpdateCounterparty(ctx context.Context, companyID, id uuid.UUID, cp Counterparty) error {
	if cp.Name == "" {
		return errors.New("store: counterparty name required")
	}
	existing, err := s.GetCounterparty(ctx, companyID, id) // enforces company scope
	if err != nil {
		return err
	}
	if existing.Erased {
		return ErrCounterpartyErased
	}
	dek, err := s.companyDEK(ctx, companyID)
	if err != nil {
		return err
	}
	enc := func(v string) (string, error) {
		if v == "" {
			return "", nil
		}
		return dek.EncryptField([]byte(v))
	}
	fields := make([]string, 6)
	for i, v := range []string{cp.Name, cp.OrgNr, cp.Personnummer, cp.Address, cp.IBAN, cp.Email} {
		e, err := enc(v)
		if err != nil {
			return err
		}
		fields[i] = e
	}
	kind := cp.Kind
	if kind != "supplier" {
		kind = "customer"
	}
	if err := s.q.UpdateCounterparty(ctx, gen.UpdateCounterpartyParams{
		ID:              id,
		CompanyID:       companyID,
		Kind:            kind,
		NameEnc:         fields[0],
		OrgnrEnc:        fields[1],
		PersonnummerEnc: fields[2],
		AddressEnc:      fields[3],
		IbanEnc:         fields[4],
		EmailEnc:        fields[5],
	}); err != nil {
		return err
	}
	return s.logAudit(ctx, s.q, companyID, "update_counterparty", "counterparty", id.String(), cp.Kind)
}

// EraseCounterparty performs a GDPR art. 17 erasure: it overwrites the identity
// ciphertext with a tombstone and stamps erased_at, keeping the row and its
// ledger links. It is refused when the counterparty is referenced by any
// non-draft (finalized or paid) invoice, because that data must be retained for
// seven years under bokföringslagen (GDPR art. 17(3)(b)). The caller should tell
// the user to retry once the retention period has lapsed.
func (s *Store) EraseCounterparty(ctx context.Context, companyID, id uuid.UUID) error {
	cp, err := s.GetCounterparty(ctx, companyID, id) // enforces company scope
	if err != nil {
		return err
	}
	if cp.Erased {
		return nil // already erased; idempotent
	}
	retained, err := s.q.CountRetainedInvoices(ctx, gen.CountRetainedInvoicesParams{CompanyID: companyID, CounterpartyID: id})
	if err != nil {
		return err
	}
	if retained > 0 {
		return ErrRetentionBlocked
	}
	dek, err := s.companyDEK(ctx, companyID)
	if err != nil {
		return err
	}
	tomb, err := dek.EncryptField([]byte(erasureTombstone))
	if err != nil {
		return err
	}
	if err := s.q.EraseCounterparty(ctx, gen.EraseCounterpartyParams{
		ID: id, CompanyID: companyID, NameEnc: tomb,
	}); err != nil {
		return err
	}
	// Purge the shield token vault so no pre-erasure identity remains resolvable
	// in any MCP session (tokens are per-session HMACs that cannot be targeted
	// individually). Active sessions re-tokenize to the tombstone on next read.
	if _, err := s.q.DeleteAllShieldTokens(ctx); err != nil {
		return err
	}
	return s.logAudit(ctx, s.q, companyID, "erase_counterparty", "counterparty", id.String(), cp.Kind)
}
