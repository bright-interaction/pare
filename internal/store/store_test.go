// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brightinteraction/pare/internal/crypto"
	"github.com/brightinteraction/pare/internal/ledger"
	"github.com/brightinteraction/pare/internal/testdb"
)

func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testdb.New(t, "store"))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	testdb.Reset(t, pool)
	key, _ := crypto.NewDEK()
	kek, err := crypto.NewKEK(key)
	if err != nil {
		t.Fatalf("kek: %v", err)
	}
	return New(pool, kek), pool
}

func day(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func TestBootstrapAndPost(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, err := s.BootstrapCompany(ctx, "Bright Interaction AB", "556000-0000")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	sale := []ledger.Line{
		{Account: "1930", Debit: ledger.SEK(12500, 0)},
		{Account: "3001", Credit: ledger.SEK(10000, 0), VATCode: "SE25"},
		{Account: "2611", Credit: ledger.SEK(2500, 0)},
	}
	verID, err := s.PostVerification(ctx, co, "A", day("2026-01-15"), "Konsultarvode", sale, uuid.Nil)
	if err != nil {
		t.Fatalf("post sale: %v", err)
	}

	cost := []ledger.Line{
		{Account: "5010", Debit: ledger.SEK(2000, 0)},
		{Account: "2640", Debit: ledger.SEK(500, 0)},
		{Account: "1930", Credit: ledger.SEK(2500, 0)},
	}
	if _, err := s.PostVerification(ctx, co, "A", day("2026-01-20"), "Lokalhyra", cost, uuid.Nil); err != nil {
		t.Fatalf("post cost: %v", err)
	}

	bad := []ledger.Line{
		{Account: "1930", Debit: ledger.SEK(100, 0)},
		{Account: "3011", Credit: ledger.SEK(99, 0)},
	}
	if _, err := s.PostVerification(ctx, co, "A", day("2026-01-21"), "bad", bad, uuid.Nil); err != ledger.ErrUnbalanced {
		t.Fatalf("want ErrUnbalanced, got %v", err)
	}

	tb, err := s.TrialBalance(ctx, co)
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	var total ledger.Amount
	for _, r := range tb {
		total += r.Net
	}
	if total != 0 {
		t.Fatalf("trial balance not zero: %s", total)
	}

	// posted verification is immutable (DB trigger)
	if _, err := pool.Exec(ctx, "UPDATE verifications SET description='tamper' WHERE id=$1", verID); err == nil {
		t.Fatal("posted verification was mutable via UPDATE")
	}
	if _, err := pool.Exec(ctx, "DELETE FROM verifications WHERE id=$1", verID); err == nil {
		t.Fatal("posted verification was deletable")
	}
}

func TestCounterpartyEncryption(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, err := s.BootstrapCompany(ctx, "BI AB", "556000-0001")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	id, err := s.CreateCounterparty(ctx, co, Counterparty{
		Kind:  "customer",
		Name:  "Advokatbyrån Nord AB",
		OrgNr: "556677-8899",
		IBAN:  "SE3550000000054910000003",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var nameEnc, orgnrEnc string
	if err := pool.QueryRow(ctx, "SELECT name_enc, orgnr_enc FROM counterparties WHERE id=$1", id).Scan(&nameEnc, &orgnrEnc); err != nil {
		t.Fatalf("read enc: %v", err)
	}
	if strings.Contains(nameEnc, "Nord") || strings.Contains(orgnrEnc, "556677") {
		t.Fatalf("identity stored in clear: name=%q orgnr=%q", nameEnc, orgnrEnc)
	}

	got, err := s.GetCounterparty(ctx, co, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Advokatbyrån Nord AB" || got.OrgNr != "556677-8899" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	other, err := s.BootstrapCompany(ctx, "Other AB", "556000-0002")
	if err != nil {
		t.Fatalf("bootstrap other: %v", err)
	}
	if _, err := s.GetCounterparty(ctx, other, id); err != ErrForeignCompany {
		t.Fatalf("want ErrForeignCompany, got %v", err)
	}
}
