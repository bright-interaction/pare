// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package handler builds Pare's HTTP router: health, the MCP endpoint, and
// (later) the operator API + embedded UI.
package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"

	"github.com/brightinteraction/pare/internal/mcp"
)

// Server holds the wired dependencies for the router.
type Server struct {
	MCP *mcp.Server
}

// Routes builds the chi router.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
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

	return r
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
