// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// shieldTokenTTL bounds how long a tokenized value lives in the shield vault.
const shieldTokenTTL = 24 * time.Hour

// nonRetainedPIIWindow is how long a never-booked bank line / orphan document is
// kept with its PII before the sweep anonymizes it. Set well beyond any plausible
// reconciliation delay (a user may take months to book a statement) but far short
// of the 7-year räkenskapsinformation rule, which does not apply to these
// non-booked records anyway.
const nonRetainedPIIWindow = 365 * 24 * time.Hour

// Sweep deletes expired sessions and stale shield tokens. It is safe to call
// repeatedly; run it at startup and on a ticker.
func (s *Store) Sweep(ctx context.Context) (sessions, tokens int64, err error) {
	sessions, err = s.q.DeleteExpiredSessions(ctx)
	if err != nil {
		return 0, 0, err
	}
	cutoff := pgtype.Timestamptz{Valid: true}
	cutoff.Time = time.Now().Add(-shieldTokenTTL)
	tokens, err = s.q.DeleteOldShieldTokens(ctx, cutoff)
	return sessions, tokens, err
}

// anonymizeNonRetainedPII sweeps every company, blanking the PII of non-retained
// records (never-booked bank lines, orphan documents) older than the retention
// window. Best-effort per company: one company's failure does not stop the rest.
func (s *Store) anonymizeNonRetainedPII(ctx context.Context) {
	companies, err := s.q.ListCompanies(ctx)
	if err != nil {
		slog.Warn("anonymize sweep: list companies failed", "err", err)
		return
	}
	cutoff := time.Now().Add(-nonRetainedPIIWindow)
	var bankTotal, docTotal int64
	for _, c := range companies {
		bank, docs, aerr := s.AnonymizeNonRetainedPII(ctx, c.ID, cutoff)
		if aerr != nil {
			slog.Warn("anonymize sweep: company failed", "company", c.ID, "err", aerr)
			continue
		}
		bankTotal += bank
		docTotal += docs
	}
	if bankTotal > 0 || docTotal > 0 {
		slog.Info("anonymize sweep", "bank_lines", bankTotal, "documents", docTotal)
	}
}

// StartSweeper runs Sweep once, then on the given interval until ctx is done.
func (s *Store) StartSweeper(ctx context.Context, every time.Duration) {
	run := func() {
		sess, tok, err := s.Sweep(ctx)
		if err != nil {
			slog.Warn("sweep failed", "err", err)
		} else if sess > 0 || tok > 0 {
			slog.Info("sweep", "expired_sessions", sess, "stale_tokens", tok)
		}
		s.anonymizeNonRetainedPII(ctx)
	}
	run()
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				run()
			}
		}
	}()
}
