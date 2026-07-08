// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Command server is the Pare entrypoint. Phase 1 wires config, logging and a
// health endpoint; the ledger store, MCP and UI are mounted in later phases.
package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/brightinteraction/pare/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level(cfg.LogLevel)})))

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	slog.Info("pare starting", "addr", cfg.Addr)
	srv := &http.Server{Addr: cfg.Addr, Handler: mux}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
