// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"testing"
)

// Sweep removes expired sessions and shield tokens older than the TTL, and
// leaves fresh rows untouched.
func TestSweep(t *testing.T) {
	s, pool := testStore(t)
	defer pool.Close()
	ctx := context.Background()

	// An expired session and a stale shield token (created 48h ago).
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES (gen_random_uuid(), 'a@b.se', 'x')`); err != nil {
		t.Fatalf("user: %v", err)
	}
	var uid string
	_ = pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&uid)
	if _, err := pool.Exec(ctx, `INSERT INTO sessions (token, user_id, expires_at) VALUES ('old', $1, now() - interval '1 hour'), ('fresh', $1, now() + interval '1 hour')`, uid); err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO shield_tokens (session_id, token, kind, ciphertext, created_at) VALUES ('s','stale','name','x', now() - interval '48 hours'), ('s','fresh','name','y', now())`); err != nil {
		t.Fatalf("tokens: %v", err)
	}

	sess, tok, err := s.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if sess != 1 || tok != 1 {
		t.Fatalf("swept sessions=%d tokens=%d, want 1/1", sess, tok)
	}
	var nSess, nTok int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&nSess)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM shield_tokens`).Scan(&nTok)
	if nSess != 1 || nTok != 1 {
		t.Fatalf("remaining sessions=%d tokens=%d, want 1/1", nSess, nTok)
	}
}
