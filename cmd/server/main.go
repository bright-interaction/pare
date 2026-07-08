// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Command server is the Pare entrypoint: it migrates the schema, wires the
// store and (when configured) the MCP, and serves the HTTP router.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brightinteraction/pare/internal/config"
	"github.com/brightinteraction/pare/internal/crypto"
	"github.com/brightinteraction/pare/internal/db"
	"github.com/brightinteraction/pare/internal/handler"
	"github.com/brightinteraction/pare/internal/mcp"
	"github.com/brightinteraction/pare/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level(cfg.LogLevel)})))

	if cfg.DatabaseURL == "" {
		slog.Error("PARE_DATABASE_URL is required")
		os.Exit(1)
	}

	ctx := context.Background()
	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("db pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	kek, err := crypto.NewKEK(cfg.MasterKey)
	if err != nil {
		slog.Error("master key", "err", err)
		os.Exit(1)
	}
	st := store.New(pool, kek)

	srv := &handler.Server{}
	if len(cfg.ShieldKey) == 32 && cfg.MCPKey != "" {
		m, err := mcp.New(st, pool, cfg.ShieldKey, cfg.MCPKey)
		if err != nil {
			slog.Error("mcp", "err", err)
			os.Exit(1)
		}
		srv.MCP = m
		slog.Info("mcp enabled at /mcp")
	} else {
		slog.Warn("mcp disabled: set PARE_SHIELD_KEY (32 bytes) and PARE_MCP_KEY to enable")
	}

	slog.Info("pare starting", "addr", cfg.Addr)
	httpSrv := &http.Server{Addr: cfg.Addr, Handler: srv.Routes()}
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

func level(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
