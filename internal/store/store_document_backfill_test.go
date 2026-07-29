// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/bright-interaction/pare/internal/crypto"
	gen "github.com/bright-interaction/pare/internal/db/generated"
)

// These tests need NO Postgres on purpose. The store integration tests skip
// unless PARE_TEST_DATABASE_URL is set and the ci-go pipeline does not set it,
// so a DB-only test of the failure policy in BackfillDocumentPII would never run
// in CI. backfillDocumentPIIRows is the same loop the exported method runs.

func testDEK(t *testing.T) *crypto.DEK {
	t.Helper()
	raw, err := crypto.NewDEK()
	if err != nil {
		t.Fatalf("new dek: %v", err)
	}
	dek, err := crypto.NewDEKFrom(raw)
	if err != nil {
		t.Fatalf("dek from raw: %v", err)
	}
	return dek
}

func backfillRow(company uuid.UUID, name string) gen.ListDocumentsForPIIBackfillRow {
	return gen.ListDocumentsForPIIBackfillRow{ID: uuid.New(), CompanyID: company, Filename: name, Note: "note " + name}
}

// TestBackfillDocumentPIISkipsOnlyUnusableDEKCompany is the repro for audit
// 2026-07-28 HIGH-2. One company with an unrecoverable dek_wrapped used to abort
// the whole backfill, and cmd/server/main.go turned that into os.Exit(1) before
// the listener existed, so the server crash-looped for every tenant. The broken
// company must now be skipped and counted while every healthy company migrates.
func TestBackfillDocumentPIISkipsOnlyUnusableDEKCompany(t *testing.T) {
	broken, healthy := uuid.New(), uuid.New()
	rows := []gen.ListDocumentsForPIIBackfillRow{
		backfillRow(broken, "kvitto_Anna_Andersson_19850101.pdf"),
		backfillRow(healthy, "faktura_ok.pdf"),
		backfillRow(broken, "kvitto_Bo_Bruten_19700101.pdf"),
		backfillRow(healthy, "faktura_ok_2.pdf"),
	}
	dek := testDEK(t)

	dekCalls := 0
	var written []gen.SetDocumentPIIEncParams
	migrated, skipped, err := backfillDocumentPIIRows(rows,
		func(company uuid.UUID) (*crypto.DEK, error) {
			dekCalls++
			if company == broken {
				return nil, fmt.Errorf("%w: unwrap dek: illegal base64 data", ErrDEKUnusable)
			}
			return dek, nil
		},
		func(p gen.SetDocumentPIIEncParams) error {
			written = append(written, p)
			return nil
		})

	if err != nil {
		t.Fatalf("an unusable company DEK must not fail the backfill, got err=%v", err)
	}
	if migrated != 2 || skipped != 2 {
		t.Fatalf("migrated=%d skipped=%d, want 2/2", migrated, skipped)
	}
	if len(written) != 2 {
		t.Fatalf("wrote %d rows, want 2 (only the healthy company)", len(written))
	}
	for _, w := range written {
		if w.FilenameEnc == "" || w.NoteEnc == "" {
			t.Fatalf("healthy row written without ciphertext: %+v", w)
		}
	}
	// The broken company is resolved once, not once per row, so a company with
	// thousands of legacy rows costs one lookup and one log line.
	if dekCalls != 2 {
		t.Fatalf("dekFor called %d times, want 2 (one per company)", dekCalls)
	}
}

// TestBackfillDocumentPIIStaysFatalOnInfrastructureFaults pins the other half of
// the failure policy: only ErrDEKUnusable is skippable. A DEK lookup that failed
// for an infrastructure reason, and a failed write, must still abort so the
// caller fails closed instead of reporting a migration it did not finish.
func TestBackfillDocumentPIIStaysFatalOnInfrastructureFaults(t *testing.T) {
	dek := testDEK(t)
	company := uuid.New()
	rows := []gen.ListDocumentsForPIIBackfillRow{
		backfillRow(company, "a.pdf"),
		backfillRow(company, "b.pdf"),
	}
	okWrite := func(gen.SetDocumentPIIEncParams) error { return nil }
	okDEK := func(uuid.UUID) (*crypto.DEK, error) { return dek, nil }

	poolErr := errors.New("conn busy: another query is already in progress")

	t.Run("dek lookup infrastructure error", func(t *testing.T) {
		migrated, skipped, err := backfillDocumentPIIRows(rows,
			func(uuid.UUID) (*crypto.DEK, error) {
				return nil, fmt.Errorf("store: get company: %w", poolErr)
			}, okWrite)
		if err == nil {
			t.Fatal("a non-DEK failure must stay fatal, got err=nil")
		}
		if errors.Is(err, ErrDEKUnusable) {
			t.Fatalf("infrastructure fault misclassified as unusable DEK: %v", err)
		}
		if !errors.Is(err, poolErr) {
			t.Fatalf("underlying cause lost: %v", err)
		}
		if migrated != 0 || skipped != 0 {
			t.Fatalf("migrated=%d skipped=%d, want 0/0", migrated, skipped)
		}
	})

	t.Run("write error", func(t *testing.T) {
		calls := 0
		migrated, skipped, err := backfillDocumentPIIRows(rows, okDEK,
			func(gen.SetDocumentPIIEncParams) error {
				calls++
				if calls == 2 {
					return poolErr
				}
				return nil
			})
		if err == nil {
			t.Fatal("a failed SetDocumentPIIEnc write must stay fatal, got err=nil")
		}
		if !errors.Is(err, poolErr) {
			t.Fatalf("underlying cause lost: %v", err)
		}
		if migrated != 1 || skipped != 0 {
			t.Fatalf("migrated=%d skipped=%d, want 1/0", migrated, skipped)
		}
	})
}

// TestDEKFromWrappedClassification pins the sentinel the loop branches on:
// unrecoverable stored bytes are ErrDEKUnusable, and a good wrapped DEK is not.
// If this classification drifts, a corrupt key silently becomes a fatal boot
// error again, or an infrastructure fault silently becomes a skip.
func TestDEKFromWrappedClassification(t *testing.T) {
	master := make([]byte, crypto.KeySize)
	for i := range master {
		master[i] = byte(i)
	}
	kek, err := crypto.NewKEK(master)
	if err != nil {
		t.Fatalf("kek: %v", err)
	}
	s := &Store{kek: kek}

	raw, err := crypto.NewDEK()
	if err != nil {
		t.Fatalf("new dek: %v", err)
	}
	wrapped, err := kek.WrapDEK(raw)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if _, err := s.dekFromWrapped(wrapped); err != nil {
		t.Fatalf("a valid wrapped DEK must unwrap: %v", err)
	}

	for name, bad := range map[string]string{
		"not base64":      "not-a-valid-wrapped-dek",
		"empty":           "",
		"wrong key/nonce": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.dekFromWrapped(bad); !errors.Is(err, ErrDEKUnusable) {
				t.Fatalf("want ErrDEKUnusable, got %v", err)
			}
		})
	}
}
