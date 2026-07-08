// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package handler

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/brightinteraction/pare/internal/auth"
	"github.com/brightinteraction/pare/internal/invoice"
	"github.com/brightinteraction/pare/internal/ledger"
	"github.com/brightinteraction/pare/internal/moms"
	"github.com/brightinteraction/pare/internal/store"
)

type ctxKey int

const (
	ctxSession ctxKey = iota
	ctxCompany
)

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(auth.CookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		info, ok := s.Auth.Validate(r.Context(), c.Value)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		company, err := s.Store.DefaultCompany(r.Context())
		if err != nil {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), ctxSession, info)
		ctx = context.WithValue(ctx, ctxCompany, company)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) base(r *http.Request, title string) pageData {
	d := pageData{Title: title}
	if info, ok := r.Context().Value(ctxSession).(auth.SessionInfo); ok {
		d.Email = info.Email
		if co, ok := r.Context().Value(ctxCompany).(uuid.UUID); ok {
			if ci, err := s.Store.Company(r.Context(), co); err == nil {
				d.CompanyName = ci.Name
			}
		}
	}
	return d
}

func companyID(r *http.Request) uuid.UUID {
	id, _ := r.Context().Value(ctxCompany).(uuid.UUID)
	return id
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	slog.Error("web handler", "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// --- auth pages ---

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if has, _ := s.Auth.HasUsers(r.Context()); !has {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if c, err := r.Cookie(auth.CookieName); err == nil {
		if _, ok := s.Auth.Validate(r.Context(), c.Value); ok {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleSetupForm(w http.ResponseWriter, r *http.Request) {
	if has, _ := s.Auth.HasUsers(r.Context()); has {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	render(w, "setup", pageData{Title: "Kom igång"}, http.StatusOK)
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if has, _ := s.Auth.HasUsers(r.Context()); has {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	email := strings.TrimSpace(r.PostFormValue("email"))
	pw := r.PostFormValue("password")
	company := strings.TrimSpace(r.PostFormValue("company"))
	orgnr := strings.TrimSpace(r.PostFormValue("orgnr"))
	if email == "" || len(pw) < 8 || company == "" {
		render(w, "setup", pageData{Title: "Kom igång", Error: "Fyll i alla fält (lösenord minst 8 tecken)."}, http.StatusBadRequest)
		return
	}
	if err := s.Auth.CreateUser(r.Context(), email, pw); err != nil {
		render(w, "setup", pageData{Title: "Kom igång", Error: "Kunde inte skapa konto."}, http.StatusBadRequest)
		return
	}
	if _, err := s.Store.BootstrapCompany(r.Context(), company, orgnr); err != nil {
		s.fail(w, err)
		return
	}
	if token, err := s.Auth.Login(r.Context(), email, pw); err == nil {
		s.Auth.SetCookie(w, token)
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	render(w, "login", pageData{Title: "Logga in"}, http.StatusOK)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	token, err := s.Auth.Login(r.Context(), strings.TrimSpace(r.PostFormValue("email")), r.PostFormValue("password"))
	if err != nil {
		render(w, "login", pageData{Title: "Logga in", Error: "Fel e-post eller lösenord."}, http.StatusUnauthorized)
		return
	}
	s.Auth.SetCookie(w, token)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		_ = s.Auth.Logout(r.Context(), c.Value)
	}
	s.Auth.ClearCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- app pages ---

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	co, ctx := companyID(r), r.Context()
	tb, err := s.Store.TrialBalance(ctx, co)
	if err != nil {
		s.fail(w, err)
		return
	}
	bal := make(map[string]ledger.Amount, len(tb))
	var result ledger.Amount
	for _, row := range tb {
		bal[row.Account] = row.Net
		if row.Class.IsResult() {
			result += row.Net
		}
	}
	d := moms.Report(bal)
	unpaid, _ := s.Store.UnpaidInvoices(ctx, co)
	var ut ledger.Amount
	for _, u := range unpaid {
		ut += u.Total
	}
	pd := s.base(r, "Översikt")
	pd.Data = struct {
		ResultKr      string
		MomsToPayKr   string
		OutputVatKr   string
		InputVatKr    string
		UnpaidCount   int
		UnpaidTotalKr string
		Moms          moms.Declaration
	}{
		ResultKr:      (-result).String(),
		MomsToPayKr:   d.Box49.String(),
		OutputVatKr:   (d.Box10 + d.Box11 + d.Box12).String(),
		InputVatKr:    d.Box48.String(),
		UnpaidCount:   len(unpaid),
		UnpaidTotalKr: ut.String(),
		Moms:          d,
	}
	render(w, "dashboard", pd, http.StatusOK)
}

func (s *Server) handleCounterparties(w http.ResponseWriter, r *http.Request) {
	cps, err := s.Store.ListCounterparties(r.Context(), companyID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	pd := s.base(r, "Kunder")
	pd.Data = cps
	render(w, "counterparties", pd, http.StatusOK)
}

func (s *Server) handleAddCounterparty(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name != "" {
		if _, err := s.Store.CreateCounterparty(r.Context(), companyID(r), store.Counterparty{
			Kind:  "customer",
			Name:  name,
			OrgNr: strings.TrimSpace(r.PostFormValue("orgnr")),
		}); err != nil {
			s.fail(w, err)
			return
		}
	}
	http.Redirect(w, r, "/counterparties", http.StatusSeeOther)
}

func (s *Server) handleInvoices(w http.ResponseWriter, r *http.Request) {
	inv, err := s.Store.ListInvoiceSummaries(r.Context(), companyID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	pd := s.base(r, "Fakturor")
	pd.Data = inv
	render(w, "invoices", pd, http.StatusOK)
}

func (s *Server) handleInvoiceNew(w http.ResponseWriter, r *http.Request) {
	cps, err := s.Store.ListCounterparties(r.Context(), companyID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	pd := s.base(r, "Ny faktura")
	pd.Data = cps
	render(w, "invoice_new", pd, http.StatusOK)
}

func (s *Server) handleInvoiceCreate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	cpID, err := uuid.Parse(r.PostFormValue("counterparty"))
	if err != nil {
		http.Redirect(w, r, "/invoices/new", http.StatusSeeOther)
		return
	}
	descs := r.PostForm["desc"]
	qtys := r.PostForm["qty"]
	prices := r.PostForm["price"]
	vats := r.PostForm["vat"]
	var lines []invoice.Line
	for i := range descs {
		d := strings.TrimSpace(descs[i])
		if d == "" {
			continue
		}
		lines = append(lines, invoice.Line{
			Description:   d,
			QuantityMilli: parseQtyMilli(get(qtys, i)),
			UnitPriceOre:  parseKrOre(get(prices, i)),
			VATCode:       moms.Code(get(vats, i)),
		})
	}
	if len(lines) == 0 {
		http.Redirect(w, r, "/invoices/new", http.StatusSeeOther)
		return
	}
	if _, err := s.Store.CreateInvoice(r.Context(), companyID(r), cpID, invoice.Invoice{Lines: lines}); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/invoices", http.StatusSeeOther)
}

func (s *Server) handleInvoiceFinalize(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	co := companyID(r)
	number := s.nextInvoiceNumber(r.Context(), co)
	if _, err := s.Store.FinalizeInvoice(r.Context(), co, id, number, time.Now(), time.Now().AddDate(0, 0, 30)); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/invoices", http.StatusSeeOther)
}

func (s *Server) handleInvoicePDF(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if s.Gotenberg == nil {
		http.Error(w, "PDF rendering not configured", http.StatusServiceUnavailable)
		return
	}
	pdf, err := s.Store.RenderInvoicePDF(r.Context(), s.Gotenberg, companyID(r), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline; filename=faktura.pdf")
	_, _ = w.Write(pdf)
}

func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	tb, err := s.Store.TrialBalance(r.Context(), companyID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	pd := s.base(r, "Rapporter")
	pd.Data = struct{ Rows []ledger.AccountBalance }{Rows: tb}
	render(w, "reports", pd, http.StatusOK)
}

func (s *Server) handleSIE(w http.ResponseWriter, r *http.Request) {
	exp, err := s.Store.ExportSIE(r.Context(), companyID(r), time.Now().UTC())
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=pare-export.se")
	if err := exp.Write(w); err != nil {
		slog.Error("sie write", "err", err)
	}
}

// --- helpers ---

func (s *Server) nextInvoiceNumber(ctx context.Context, co uuid.UUID) string {
	inv, _ := s.Store.ListInvoiceSummaries(ctx, co)
	seq := 1
	for _, i := range inv {
		if i.Number != "" {
			seq++
		}
	}
	return fmt.Sprintf("%d-%04d", time.Now().Year(), seq)
}

func get(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

func parseQtyMilli(s string) int64 {
	if s == "" {
		return 1000
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	if err != nil {
		return 1000
	}
	return int64(math.Round(f * 1000))
}

func parseKrOre(s string) ledger.Amount {
	f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	if err != nil {
		return 0
	}
	return ledger.Amount(math.Round(f * 100))
}
