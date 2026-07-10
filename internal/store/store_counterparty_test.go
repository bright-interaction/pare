// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
package store

import (
	"context"
	"strings"
	"testing"

	"github.com/bright-interaction/pare/internal/invoice"
	"github.com/bright-interaction/pare/internal/ledger"
	"github.com/bright-interaction/pare/internal/moms"
)

func TestUpdateCounterparty(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")
	id, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "customer", Name: "Gammalt Namn AB", OrgNr: "556100-0001"})

	err := s.UpdateCounterparty(ctx, co, id, Counterparty{
		Kind: "supplier", Name: "Nytt Namn AB", OrgNr: "556100-0002",
		Personnummer: "19900101-1234", Address: "Storgatan 1", IBAN: "SE3550000000054910000003",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got, _ := s.GetCounterparty(ctx, co, id)
	if got.Name != "Nytt Namn AB" || got.OrgNr != "556100-0002" || got.Kind != "supplier" {
		t.Fatalf("update not applied: %+v", got)
	}
	if got.Personnummer != "19900101-1234" || got.Address != "Storgatan 1" {
		t.Fatalf("PII fields not updated: %+v", got)
	}

	// The new values must be ciphertext at rest.
	var nameEnc, pnrEnc string
	_ = pool.QueryRow(ctx, "SELECT name_enc, personnummer_enc FROM counterparties WHERE id=$1", id).Scan(&nameEnc, &pnrEnc)
	if strings.Contains(nameEnc, "Nytt") || strings.Contains(pnrEnc, "19900101") {
		t.Fatalf("plaintext leaked at rest: name=%q pnr=%q", nameEnc, pnrEnc)
	}
}

func TestEraseCounterpartyAllowedScrubsPII(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")
	id, _ := s.CreateCounterparty(ctx, co, Counterparty{
		Kind: "customer", Name: "Radera Mig AB", OrgNr: "556100-9999",
		Personnummer: "19850101-4321", Address: "Hemgatan 5", IBAN: "SE3550000000054910000003",
	})

	if err := s.EraseCounterparty(ctx, co, id); err != nil {
		t.Fatalf("erase: %v", err)
	}

	got, _ := s.GetCounterparty(ctx, co, id)
	if !got.Erased {
		t.Fatal("not marked erased")
	}
	if got.OrgNr != "" || got.Personnummer != "" || got.Address != "" || got.IBAN != "" {
		t.Fatalf("PII not scrubbed: %+v", got)
	}
	if !strings.Contains(got.Name, "raderad") {
		t.Fatalf("name not tombstoned: %q", got.Name)
	}

	// No original identity remains in any ciphertext column.
	var name, orgnr, pnr, addr, iban string
	_ = pool.QueryRow(ctx, "SELECT name_enc, orgnr_enc, personnummer_enc, address_enc, iban_enc FROM counterparties WHERE id=$1", id).
		Scan(&name, &orgnr, &pnr, &addr, &iban)
	for _, leak := range []string{"Radera", "556100-9999", "19850101", "Hemgatan", "SE355"} {
		joined := name + orgnr + pnr + addr + iban
		if strings.Contains(joined, leak) {
			t.Fatalf("erased row still holds %q", leak)
		}
	}

	// Idempotent second erase.
	if err := s.EraseCounterparty(ctx, co, id); err != nil {
		t.Fatalf("second erase should be a no-op, got %v", err)
	}
}

func TestEraseCounterpartyBlockedByRetention(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")
	cust, _ := s.CreateCounterparty(ctx, co, Counterparty{Kind: "customer", Name: "Bokförd Kund AB", OrgNr: "556100-1111"})

	invID, _ := s.CreateInvoice(ctx, co, cust, invoice.Invoice{Lines: []invoice.Line{
		{Description: "Tjänst", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(1000, 0), VATCode: moms.SE25},
	}})
	if _, _, err := s.FinalizeInvoice(ctx, co, invID, day("2026-02-01"), day("2026-03-03")); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	if err := s.EraseCounterparty(ctx, co, cust); err != ErrRetentionBlocked {
		t.Fatalf("want ErrRetentionBlocked, got %v", err)
	}
	// Identity must be untouched after a blocked erase.
	got, _ := s.GetCounterparty(ctx, co, cust)
	if got.Erased || got.Name != "Bokförd Kund AB" {
		t.Fatalf("blocked erase mutated identity: %+v", got)
	}
}
