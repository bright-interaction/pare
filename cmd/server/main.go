// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

// Command server is the Pare entrypoint: it migrates the schema, wires the
// store and (when configured) the MCP, and serves the HTTP router.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bright-interaction/pare/internal/auth"
	"github.com/bright-interaction/pare/internal/config"
	"github.com/bright-interaction/pare/internal/crypto"
	"github.com/bright-interaction/pare/internal/db"
	gen "github.com/bright-interaction/pare/internal/db/generated"
	"github.com/bright-interaction/pare/internal/email"
	"github.com/bright-interaction/pare/internal/flarereport"
	"github.com/bright-interaction/pare/internal/handler"
	"github.com/bright-interaction/pare/internal/mcp"
	"github.com/bright-interaction/pare/internal/render"
	"github.com/bright-interaction/pare/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level(cfg.LogLevel)})))

	// Error reporting to the house Flare instance (no-op unless FLARE_DSN is set,
	// which the Hephaestus deploy step injects).
	release := os.Getenv("PARE_RELEASE")
	if release == "" {
		release = "dev"
	}
	flarereport.InitFlare("pare", release)
	defer flarereport.Flush()

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

	// Backfill any chart accounts added since a company was bootstrapped (e.g.
	// the currency-difference accounts) so existing books self-heal on deploy.
	if err := st.SyncChart(ctx); err != nil {
		slog.Error("sync chart", "err", err)
		os.Exit(1)
	}

	// Encrypt any document filename/note still stored in cleartext (rows written
	// before those columns were encrypted), then blank the cleartext.
	//
	// A company whose DEK is unusable is skipped, not fatal (audit 2026-07-28
	// HIGH-2: one corrupt dek_wrapped used to crash-loop the whole multi-tenant
	// server on every restart). Every other backfill failure, including a failed
	// selection query or a failed write, is still fatal here: those are transient
	// or structural faults, and booting on them would silently leave PII in
	// cleartext with the migration reported as done.
	migrated, skipped, err := st.BackfillDocumentPII(ctx)
	if err != nil {
		slog.Error("backfill document pii", "err", err)
		flarereport.CaptureErr(err)
		flarereport.Flush() // the deferred Flush above does not run under os.Exit
		os.Exit(1)
	}
	if migrated > 0 {
		slog.Info("backfilled document pii encryption", "documents", migrated)
	}
	if skipped > 0 {
		// Loud on purpose. These rows keep cleartext PII (a receipt filename
		// routinely carries a name or personnummer), and migration 00025's plan to
		// drop the cleartext columns is only safe once this count is zero. Error
		// level plus a Flare issue, matching the handler convention for a
		// handled-but-should-page condition, so it does not depend on log shipping.
		slog.Error("backfill document pii incomplete: documents left in cleartext",
			"documents", skipped)
		flarereport.CaptureErr(fmt.Errorf(
			"backfill document pii: %d document(s) left in cleartext, a company DEK is unusable", skipped))
	}

	// Sweep expired sessions + stale shield tokens hourly (bounds tokenized-value
	// lifetime, incl. GDPR-erased identities captured in old MCP sessions).
	st.StartSweeper(ctx, time.Hour)

	secureCookies := os.Getenv("PARE_INSECURE_COOKIES") != "1"
	mailer := email.New(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom, cfg.SMTPFromName, cfg.SMTPTLS)
	if mailer.Enabled() {
		slog.Info("email enabled", "from", cfg.SMTPFrom)
	}
	srv := &handler.Server{
		Store:         st,
		Auth:          auth.New(gen.New(pool), secureCookies),
		Gotenberg:     render.NewGotenberg(cfg.GotenbergURL),
		Mailer:        mailer,
		SecureCookies: secureCookies,
	}
	if len(cfg.ShieldKey) == 32 && cfg.MCPKey != "" {
		m, err := mcp.New(st, pool, cfg.ShieldKey, cfg.MCPKey, cfg.MCPMaxOre)
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
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second, // Slowloris mitigation
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second, // PDF render can take up to ~60s
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
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
