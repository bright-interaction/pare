// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package shield

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	gen "github.com/bright-interaction/pare/internal/db/generated"
)

// PgStore is the Postgres-backed token vault.
type PgStore struct{ q *gen.Queries }

// NewPgStore builds a PgStore over the sqlc queries.
func NewPgStore(q *gen.Queries) *PgStore { return &PgStore{q: q} }

// Put upserts a token's ciphertext (first writer wins for a stable id).
func (p *PgStore) Put(ctx context.Context, sessionID, token, kind, ciphertext string) error {
	return p.q.UpsertShieldToken(ctx, gen.UpsertShieldTokenParams{
		SessionID:  sessionID,
		Token:      token,
		Kind:       kind,
		Ciphertext: ciphertext,
	})
}

// Get returns a token's ciphertext, or "" if it is not in the vault.
func (p *PgStore) Get(ctx context.Context, sessionID, token string) (string, error) {
	ct, err := p.q.GetShieldToken(ctx, gen.GetShieldTokenParams{SessionID: sessionID, Token: token})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return ct, err
}
