// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package db embeds the goose migrations and exposes a programmatic runner so
// the server applies schema on boot without a goose binary.
package db

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx stdlib driver for goose
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies all up migrations against the given Postgres DSN.
func Migrate(dsn string) error {
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("db: open: %w", err)
	}
	defer d.Close()

	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("db: dialect: %w", err)
	}
	if err := goose.Up(d, "migrations"); err != nil {
		return fmt.Errorf("db: migrate: %w", err)
	}
	return nil
}
