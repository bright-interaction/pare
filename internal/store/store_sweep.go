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

// StartSweeper runs Sweep once, then on the given interval until ctx is done.
func (s *Store) StartSweeper(ctx context.Context, every time.Duration) {
	run := func() {
		sess, tok, err := s.Sweep(ctx)
		if err != nil {
			slog.Warn("sweep failed", "err", err)
			return
		}
		if sess > 0 || tok > 0 {
			slog.Info("sweep", "expired_sessions", sess, "stale_tokens", tok)
		}
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
