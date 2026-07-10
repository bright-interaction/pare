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

	"github.com/bright-interaction/pare/internal/auth"
	"github.com/bright-interaction/pare/internal/email"
	"github.com/bright-interaction/pare/internal/flarereport"
	"github.com/bright-interaction/pare/internal/mcp"
	renderpkg "github.com/bright-interaction/pare/internal/render"
	"github.com/bright-interaction/pare/internal/store"
)

// Server holds the wired dependencies for the router.
type Server struct {
	MCP       *mcp.Server
	Auth      *auth.Auth
	Store     *store.Store
	Gotenberg *renderpkg.Gotenberg
	Mailer    *email.Mailer
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
	r.Use(flarereport.FlareRecoverer)
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
				r.Use(s.blockViewerWrites)
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
				r.Post("/invoices/{id}/delete", s.handleInvoiceDelete)
				r.Post("/invoices/{id}/credit", s.handleInvoiceCredit)
				r.Post("/invoices/{id}/send", s.handleInvoiceSend)
				r.Post("/invoices/{id}/remind", s.handleInvoiceRemind)
				r.Get("/invoices/{id}/pay", s.handlePayForm)
				r.Post("/invoices/{id}/pay", s.handlePay)
				r.Get("/invoices/{id}/pdf", s.handleInvoicePDF)
				r.Get("/kvitton", s.handleReceipts)
				r.Post("/kvitton", s.handleReceiptUpload)
				r.Get("/kvitton/{id}/file", s.handleReceiptFile)
				r.Post("/kvitton/{id}/delete", s.handleReceiptDelete)
				r.Get("/supplier-invoices", s.handleSupplierInvoices)
				r.Get("/supplier-invoices/new", s.handleSupplierInvoiceNew)
				r.Post("/supplier-invoices", s.handleSupplierInvoiceCreate)
				r.Post("/supplier-invoices/{id}/finalize", s.handleSupplierInvoiceFinalize)
				r.Get("/supplier-invoices/{id}/pay", s.handleSupplierPayForm)
				r.Post("/supplier-invoices/{id}/pay", s.handleSupplierPay)
				r.Get("/verifications", s.handleVerifications)
				r.Post("/verifications", s.handlePostVerification)
				r.Post("/verifications/{id}/undo", s.handleUndo)
				r.Get("/reports", s.handleReports)
				r.Get("/reskontra", s.handleReskontra)
				r.Get("/bank", s.handleBank)
				r.Post("/bank/import", s.handleBankImport)
				r.Post("/bank/{id}/book-invoice", s.handleBankBookInvoice)
				r.Post("/bank/{id}/book-account", s.handleBankBookAccount)
				r.Post("/bank/{id}/ignore", s.handleBankIgnore)
				r.Get("/bokslut", s.handleFiscalYears)
				r.Post("/bokslut", s.handleAddFiscalYear)
				r.Post("/bokslut/{id}/close", s.handleCloseFiscalYear)
				r.Get("/sie", s.handleSIE)
				r.Get("/sie/import", s.handleSIEImportForm)
				r.Post("/sie/import", s.handleSIEImport)
				r.Get("/export.csv", s.handleCSV)
				r.Get("/logg", s.handleLogg)
				r.Post("/lock", s.handleLock)
				r.Post("/unlock", s.handleUnlock)
				r.Get("/settings", s.handleSettings)
				r.Post("/settings", s.handleSettingsSave)
				r.Post("/users", s.handleInviteUser)
				r.Get("/hjalp", s.handleHelp)
				r.Get("/api", s.handleAPI)
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
		limit := int64(1 << 20)
		switch {
		case r.URL.Path == "/sie/import": // a full year's SIE file
			limit = 16 << 20
		case r.URL.Path == "/kvitton" || r.URL.Path == "/supplier-invoices": // receipt uploads
			limit = 16 << 20
		case r.URL.Path == "/bank/import": // bank statement upload
			limit = 16 << 20
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}
