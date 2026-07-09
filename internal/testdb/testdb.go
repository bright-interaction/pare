// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package testdb gives each test package its own migrated Postgres database so
// `go test ./...` (which runs packages in parallel) never has two suites racing
// on the same tables. Set PARE_TEST_DATABASE_URL to a base DSN; New derives a
// per-suffix database from it.
package testdb

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brightinteraction/pare/internal/db"
)

// New creates (if needed) and migrates a database named pare_<suffix> off the
// base DSN, and returns its DSN. The test is skipped if PARE_TEST_DATABASE_URL
// is unset.
func New(t *testing.T, suffix string) string {
	t.Helper()
	base := os.Getenv("PARE_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("PARE_TEST_DATABASE_URL not set; skipping DB integration test")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("testdb: parse base dsn: %v", err)
	}
	name := "pare_" + suffix

	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("testdb: connect: %v", err)
	}
	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists); err != nil {
		t.Fatalf("testdb: check db: %v", err)
	}
	if !exists {
		if _, err := admin.Exec(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
			t.Fatalf("testdb: create db: %v", err)
		}
	}
	_ = admin.Close(ctx)

	u.Path = "/" + name
	dsn := u.String()
	if err := db.Migrate(dsn); err != nil {
		t.Fatalf("testdb: migrate: %v", err)
	}
	return dsn
}

// Reset truncates all business tables so each test starts clean.
func Reset(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	// companies CASCADE clears accounts/verifications/counterparties/invoices/
	// supplier_invoices/audit_log/fiscal_years; users CASCADE clears sessions;
	// shield_tokens stands alone. Truncate all so every test starts fully isolated.
	if _, err := pool.Exec(context.Background(), "TRUNCATE companies, users, shield_tokens CASCADE"); err != nil {
		t.Fatalf("testdb: reset: %v", err)
	}
}
