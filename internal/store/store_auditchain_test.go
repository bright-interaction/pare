// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"testing"

	"github.com/bright-interaction/pare/internal/ledger"
)

// The audit hash chain is intact after normal activity and detects a tampered
// entry.
func TestAuditChainTamperDetection(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()
	co, _ := s.BootstrapCompany(ctx, "BI AB", "556000-0000")
	// A few audited actions.
	s.CreateCounterparty(ctx, co, Counterparty{Kind: "customer", Name: "A", OrgNr: "1"})
	s.PostVerification(ctx, co, "A", day("2026-01-15"), "x", []ledger.Line{
		{Account: "1930", Debit: ledger.SEK(100, 0)}, {Account: "3001", Credit: ledger.SEK(100, 0)},
	}, [16]byte{})

	ok, broken, err := s.VerifyAuditChain(ctx, co)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatalf("chain should be intact, broken at %d", broken)
	}

	// Tamper with an entry's detail directly (simulating a DB edit).
	if _, err := pool.Exec(ctx, "UPDATE audit_log SET detail='tampered' WHERE company_id=$1 AND id=(SELECT min(id) FROM audit_log WHERE company_id=$1)", co); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	ok, broken, _ = s.VerifyAuditChain(ctx, co)
	if ok {
		t.Fatal("tampering not detected")
	}
	if broken == 0 {
		t.Fatal("no broken id reported")
	}
}
