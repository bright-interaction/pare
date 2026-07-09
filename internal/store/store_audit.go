// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/brightinteraction/pare/internal/db/generated"
	"github.com/brightinteraction/pare/internal/ledger"
)

// ErrPeriodClosed is returned when a posting falls in a locked period.
var ErrPeriodClosed = errors.New("store: the accounting period is closed for that date; post the correction in the current period")

// Actor identifies who is performing a write, for the audit log.
type Actor struct {
	Kind   string // "user", "ai", or "system"
	Detail string // e.g. the user email, or "mcp"
}

type actorKey struct{}

// WithActor attaches the acting party to the context so store writes attribute
// the audit log correctly. Web handlers set user; the MCP sets ai.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

func actorFrom(ctx context.Context) Actor {
	if a, ok := ctx.Value(actorKey{}).(Actor); ok && a.Kind != "" {
		return a
	}
	return Actor{Kind: "system"}
}

func (s *Store) logAudit(ctx context.Context, q *gen.Queries, companyID uuid.UUID, action, targetType, targetID, detail string) error {
	a := actorFrom(ctx)
	return q.InsertAuditLog(ctx, gen.InsertAuditLogParams{
		CompanyID:   companyID,
		Actor:       a.Kind,
		ActorDetail: a.Detail,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		Detail:      detail,
	})
}

// LockedThrough returns the current period-lock boundary, if any.
func (s *Store) LockedThrough(ctx context.Context, companyID uuid.UUID) (time.Time, bool, error) {
	co, err := s.q.GetCompany(ctx, companyID)
	if err != nil {
		return time.Time{}, false, err
	}
	return co.LockedThrough.Time, co.LockedThrough.Valid, nil
}

// LockPeriod closes everything on or before through, logged with a reason.
func (s *Store) LockPeriod(ctx context.Context, companyID uuid.UUID, through time.Time, reason string) error {
	return s.inTx(ctx, func(qtx *gen.Queries) error {
		if err := qtx.SetLockedThrough(ctx, gen.SetLockedThroughParams{ID: companyID, LockedThrough: pgDate(through)}); err != nil {
			return err
		}
		return s.logAudit(ctx, qtx, companyID, "lock_period", "period", through.Format("2006-01-02"), reason)
	})
}

// UnlockPeriod clears the lock. reason is required and logged. Never exposed on
// the MCP: only a human can unlock.
func (s *Store) UnlockPeriod(ctx context.Context, companyID uuid.UUID, reason string) error {
	return s.inTx(ctx, func(qtx *gen.Queries) error {
		if err := qtx.SetLockedThrough(ctx, gen.SetLockedThroughParams{ID: companyID, LockedThrough: pgtype.Date{}}); err != nil {
			return err
		}
		return s.logAudit(ctx, qtx, companyID, "unlock_period", "period", "", reason)
	})
}

// AuditEntry is one row of the audit log for display.
type AuditEntry struct {
	At          time.Time
	Actor       string
	ActorDetail string
	Action      string
	TargetType  string
	TargetID    string
	Detail      string
}

// ListAudit returns the most recent audit entries.
func (s *Store) ListAudit(ctx context.Context, companyID uuid.UUID, limit int) ([]AuditEntry, error) {
	rows, err := s.q.ListAuditLog(ctx, gen.ListAuditLogParams{CompanyID: companyID, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]AuditEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, AuditEntry{
			At:          r.At.Time,
			Actor:       r.Actor,
			ActorDetail: r.ActorDetail,
			Action:      r.Action,
			TargetType:  r.TargetType,
			TargetID:    r.TargetID,
			Detail:      r.Detail,
		})
	}
	return out, nil
}

// VerificationLineView is one posted line (for the readable verifikationslista).
type VerificationLineView struct {
	Account string
	Debit   ledger.Amount
	Credit  ledger.Amount
}

// VerificationSummary is a verifikat header (+ its lines) for lists and the
// statutory readable-form verifikationslista.
type VerificationSummary struct {
	ID          uuid.UUID
	Series      string
	Number      int
	Date        string
	Description string
	IsReversal  bool
	Lines       []VerificationLineView
}

// ListVerificationSummaries lists posted verifikat with their transaction lines,
// newest date last.
func (s *Store) ListVerificationSummaries(ctx context.Context, companyID uuid.UUID) ([]VerificationSummary, error) {
	vers, err := s.q.ListVerifications(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]VerificationSummary, 0, len(vers))
	for _, v := range vers {
		sum := VerificationSummary{
			ID:          v.ID,
			Series:      v.Series,
			Number:      int(v.Number),
			Date:        v.Vdate.Time.Format("2006-01-02"),
			Description: v.Description,
			IsReversal:  v.ReversalOf.Valid,
		}
		lineRows, err := s.q.ListVerificationLinesByVerification(ctx, v.ID)
		if err != nil {
			return nil, err
		}
		for _, l := range lineRows {
			sum.Lines = append(sum.Lines, VerificationLineView{
				Account: l.Account, Debit: ledger.Amount(l.DebitOre), Credit: ledger.Amount(l.CreditOre),
			})
		}
		out = append(out, sum)
	}
	return out, nil
}

// UndoVerification posts a reversing entry (rättelseverifikat, series R) dated
// today so the correction lands in the current period, and logs the undo. The
// original verifikat stays immutable.
func (s *Store) UndoVerification(ctx context.Context, companyID, verID uuid.UUID) (uuid.UUID, error) {
	ver, err := s.q.GetVerification(ctx, verID)
	if err != nil {
		return uuid.Nil, err
	}
	if ver.CompanyID != companyID {
		return uuid.Nil, ErrForeignCompany
	}
	lineRows, err := s.q.ListVerificationLinesByVerification(ctx, verID)
	if err != nil {
		return uuid.Nil, err
	}
	orig := ledger.Verification{Series: ver.Series, Number: int(ver.Number)}
	for _, l := range lineRows {
		orig.Lines = append(orig.Lines, ledger.Line{
			Account: l.Account,
			Debit:   ledger.Amount(l.DebitOre),
			Credit:  ledger.Amount(l.CreditOre),
			VATCode: l.VatCode,
		})
	}
	rev := ledger.Reverse(orig, "R", 0, time.Now(), "Ångra "+orig.ID())

	var revID uuid.UUID
	err = s.inTx(ctx, func(qtx *gen.Queries) error {
		id, err := s.postVerification(ctx, qtx, companyID, "R", time.Now(), rev.Description, rev.Lines, verID)
		if err != nil {
			return err
		}
		revID = id
		return s.logAudit(ctx, qtx, companyID, "undo", "verification", verID.String(), "rättelse "+id.String())
	})
	return revID, err
}
