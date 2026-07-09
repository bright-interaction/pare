// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/brightinteraction/pare/internal/ledger"
)

func sale() []ledger.Line {
	return []ledger.Line{
		{Account: "1930", Debit: ledger.SEK(12500, 0)},
		{Account: "3001", Credit: ledger.SEK(10000, 0)},
		{Account: "2611", Credit: ledger.SEK(2500, 0)},
	}
}

func TestPeriodLockUnknownAccountAndAudit(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	co, err := s.BootstrapCompany(ctx, "BI AB", "556000-0000")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// post as the AI; audit must attribute actor=ai
	aiCtx := WithActor(ctx, Actor{Kind: "ai", Detail: "mcp"})
	verID, err := s.PostVerification(aiCtx, co, "A", day("2026-01-15"), "Sale", sale(), uuid.Nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	entries, err := s.ListAudit(ctx, co, 10)
	if err != nil || len(entries) == 0 || entries[0].Actor != "ai" || entries[0].Action != "post_verification" {
		t.Fatalf("audit not attributed to ai: %+v (%v)", entries, err)
	}

	// lock through Jan 31: a January posting is now refused, February is fine
	if err := s.LockPeriod(ctx, co, day("2026-01-31"), "månadsavslut"); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := s.PostVerification(ctx, co, "A", day("2026-01-20"), "late", sale(), uuid.Nil); !errors.Is(err, ErrPeriodClosed) {
		t.Fatalf("want ErrPeriodClosed, got %v", err)
	}
	if _, err := s.PostVerification(ctx, co, "A", day("2026-02-01"), "feb", sale(), uuid.Nil); err != nil {
		t.Fatalf("feb post: %v", err)
	}

	// an account not in the chart is rejected (the AI cannot invent accounts)
	bad := []ledger.Line{{Account: "9999", Debit: ledger.SEK(1, 0)}, {Account: "1930", Credit: ledger.SEK(1, 0)}}
	if _, err := s.PostVerification(ctx, co, "A", day("2026-02-02"), "bad", bad, uuid.Nil); !errors.Is(err, ErrUnknownAccount) {
		t.Fatalf("want ErrUnknownAccount, got %v", err)
	}

	// undo posts a reversing entry (dated today, current period) + logs "undo"
	revID, err := s.UndoVerification(ctx, co, verID)
	if err != nil || revID == uuid.Nil {
		t.Fatalf("undo: %v", err)
	}
	tb, _ := s.TrialBalance(ctx, co)
	var total ledger.Amount
	for _, r := range tb {
		total += r.Net
	}
	if total != 0 {
		t.Fatalf("trial balance not zero after undo: %s", total)
	}
	entries, _ = s.ListAudit(ctx, co, 30)
	foundUndo := false
	for _, e := range entries {
		if e.Action == "undo" && e.TargetID == verID.String() {
			foundUndo = true
		}
	}
	if !foundUndo {
		t.Fatal("no undo entry in audit log")
	}
}
