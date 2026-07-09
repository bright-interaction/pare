// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package handler builds Pare's HTTP router: health, the MCP endpoint, and the
// server-rendered operator UI (session-authed).
package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"

	"github.com/brightinteraction/pare/internal/auth"
	"github.com/brightinteraction/pare/internal/mcp"
	renderpkg "github.com/brightinteraction/pare/internal/render"
	"github.com/brightinteraction/pare/internal/store"
)

// Server holds the wired dependencies for the router.
type Server struct {
	MCP       *mcp.Server
	Auth      *auth.Auth
	Store     *store.Store
	Gotenberg *renderpkg.Gotenberg
	// SecureCookies marks auth/CSRF cookies Secure (mirrors the auth setting;
	// disabled only for local plain-HTTP dev via PARE_INSECURE_COOKIES=1).
	SecureCookies bool
}

// Routes builds the chi router.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(maxBody)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(securityHeaders)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// MCP: JSON-RPC over POST. Auth (org key) is enforced inside the handler.
	if s.MCP != nil {
		r.Route("/mcp", func(r chi.Router) {
			r.Use(httprate.LimitByIP(240, time.Minute))
			r.Handle("/", s.MCP.Handler())
			r.Handle("/*", s.MCP.Handler())
		})
	}

	// Operator web UI (server-rendered, session cookie auth).
	if s.Auth != nil && s.Store != nil {
		// Strict per-IP limit on the auth surface (brute-force / abuse).
		authLimit := httprate.LimitByIP(10, time.Minute)
		r.Group(func(r chi.Router) {
			// Synchronizer-token CSRF on the whole form UI, GET (issue) and
			// POST (verify).
			r.Use(s.csrfProtect)
			r.Get("/", s.handleRoot)
			r.Get("/setup", s.handleSetupForm)
			r.With(authLimit).Post("/setup", s.handleSetup)
			r.Get("/login", s.handleLoginForm)
			r.With(authLimit).Post("/login", s.handleLogin)
			r.With(authLimit).Post("/logout", s.handleLogout)

			r.Group(func(r chi.Router) {
				r.Use(s.requireSession)
				r.Get("/dashboard", s.handleDashboard)
				r.Get("/counterparties", s.handleCounterparties)
				r.Post("/counterparties", s.handleAddCounterparty)
				r.Get("/counterparties/{id}/edit", s.handleCounterpartyEdit)
				r.Post("/counterparties/{id}", s.handleCounterpartyUpdate)
				r.Post("/counterparties/{id}/erase", s.handleCounterpartyErase)
				r.Get("/invoices", s.handleInvoices)
				r.Get("/invoices/new", s.handleInvoiceNew)
				r.Post("/invoices", s.handleInvoiceCreate)
				r.Post("/invoices/{id}/finalize", s.handleInvoiceFinalize)
				r.Get("/invoices/{id}/pay", s.handlePayForm)
				r.Post("/invoices/{id}/pay", s.handlePay)
				r.Get("/invoices/{id}/pdf", s.handleInvoicePDF)
				r.Get("/verifications", s.handleVerifications)
				r.Post("/verifications", s.handlePostVerification)
				r.Post("/verifications/{id}/undo", s.handleUndo)
				r.Get("/reports", s.handleReports)
				r.Get("/sie", s.handleSIE)
				r.Get("/export.csv", s.handleCSV)
				r.Get("/logg", s.handleLogg)
				r.Post("/lock", s.handleLock)
				r.Post("/unlock", s.handleUnlock)
			})
		})
	}

	return r
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// script-src falls back to default-src 'none' (no inline/remote JS in
		// the app); the UI's single inline <style> is allowed via style-src.
		h.Set("Content-Security-Policy",
			"default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}

// maxBody caps every request body at 1 MiB so ParseForm and JSON decoding can't
// be used to exhaust memory (the /mcp handler additionally caps its own body).
func maxBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}
