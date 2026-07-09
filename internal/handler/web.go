// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package handler

import (
	"context"
	"encoding/csv"
	"errors"
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
	"github.com/brightinteraction/pare/internal/flarereport"
	"github.com/brightinteraction/pare/internal/invoice"
	"github.com/brightinteraction/pare/internal/ledger"
	"github.com/brightinteraction/pare/internal/moms"
	"github.com/brightinteraction/pare/internal/sie"
	"github.com/brightinteraction/pare/internal/store"
)

type ctxKey int

const (
	ctxSession ctxKey = iota
	ctxCompany
)

// blockViewerWrites rejects state-changing requests from a read-only viewer
// (revisor) role. Mount it after requireSession so the role is in context.
func (s *Server) blockViewerWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) {
			if info, ok := r.Context().Value(ctxSession).(auth.SessionInfo); ok && !info.IsOwner() {
				http.Error(w, "read-only (revisor) account", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

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
		ctx = store.WithActor(ctx, store.Actor{Kind: "user", Detail: info.Email})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) base(r *http.Request, title string) pageData {
	d := pageData{Title: title, CSRF: csrfToken(r)}
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
	flarereport.CaptureErr(err)
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
	render(w, "setup", pageData{Title: "Kom igång", CSRF: csrfToken(r)}, http.StatusOK)
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
		render(w, "setup", pageData{Title: "Kom igång", CSRF: csrfToken(r), Error: "Fyll i alla fält (lösenord minst 8 tecken)."}, http.StatusBadRequest)
		return
	}
	// Company first: a DB singleton (migration 00008) makes a concurrent second
	// BootstrapCompany fail here, before any user is created, closing the
	// first-user-wins race without an orphan user.
	coID, err := s.Store.BootstrapCompany(r.Context(), company, orgnr)
	if err != nil {
		render(w, "setup", pageData{Title: "Kom igång", CSRF: csrfToken(r), Error: "Kunde inte skapa företaget (det kan redan finnas ett)."}, http.StatusBadRequest)
		return
	}
	// Seller profile for valid invoices (best-effort; editable later under Företag).
	_ = s.Store.UpdateCompanyProfile(r.Context(), companyProfileFromForm(r, coID, company, orgnr))
	if err := s.Auth.CreateUser(r.Context(), email, pw, "owner"); err != nil {
		render(w, "setup", pageData{Title: "Kom igång", CSRF: csrfToken(r), Error: "Kunde inte skapa konto."}, http.StatusBadRequest)
		return
	}
	if token, err := s.Auth.Login(r.Context(), email, pw); err == nil {
		s.Auth.SetCookie(w, token)
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	render(w, "login", pageData{Title: "Logga in", CSRF: csrfToken(r)}, http.StatusOK)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	token, err := s.Auth.Login(r.Context(), strings.TrimSpace(r.PostFormValue("email")), r.PostFormValue("password"))
	if err != nil {
		render(w, "login", pageData{Title: "Logga in", CSRF: csrfToken(r), Error: "Fel e-post eller lösenord."}, http.StatusUnauthorized)
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
		ut += u.TotalSEK
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

// companyProfileFromForm builds a CompanyInfo from the setup/settings form,
// deriving the momsregnr from the org number when the field is left blank.
func companyProfileFromForm(r *http.Request, id uuid.UUID, name, orgnr string) store.CompanyInfo {
	momsregnr := strings.TrimSpace(r.PostFormValue("momsregnr"))
	if momsregnr == "" {
		momsregnr = deriveMomsRegNr(orgnr)
	}
	return store.CompanyInfo{
		ID: id, Name: name, OrgNr: orgnr, MomsRegNr: momsregnr,
		Address:    strings.TrimSpace(r.PostFormValue("address")),
		PostalCode: strings.TrimSpace(r.PostFormValue("postal_code")),
		City:       strings.TrimSpace(r.PostFormValue("city")),
		Bankgiro:   strings.TrimSpace(r.PostFormValue("bankgiro")),
		IBAN:       strings.TrimSpace(r.PostFormValue("iban")),
		FSkatt:     r.PostFormValue("fskatt") == "1",
	}
}

// deriveMomsRegNr builds the Swedish VAT number SE<10 digits>01 from an org
// number (returns "" if the org number is not 10 digits).
func deriveMomsRegNr(orgnr string) string {
	digits := make([]rune, 0, 10)
	for _, c := range orgnr {
		if c >= '0' && c <= '9' {
			digits = append(digits, c)
		}
	}
	if len(digits) != 10 {
		return ""
	}
	return "SE" + string(digits) + "01"
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	ci, err := s.Store.Company(r.Context(), companyID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	pd := s.base(r, "Företag")
	switch r.URL.Query().Get("msg") {
	case "saved":
		pd.Flash = "Företagsuppgifterna sparades."
	case "userok":
		pd.Flash = "Revisorskontot skapades (läsbehörighet)."
	case "userbad":
		pd.Error = "Ange e-post och lösenord (minst 8 tecken)."
	case "userexists":
		pd.Error = "Kunde inte skapa kontot (e-posten kan redan finnas)."
	}
	pd.Data = ci
	render(w, "settings", pd, http.StatusOK)
}

func (s *Server) handleInviteUser(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	email := strings.TrimSpace(r.PostFormValue("email"))
	pw := r.PostFormValue("password")
	if email == "" || len(pw) < 8 {
		http.Redirect(w, r, "/settings?msg=userbad", http.StatusSeeOther)
		return
	}
	if err := s.Auth.CreateUser(r.Context(), email, pw, "viewer"); err != nil {
		http.Redirect(w, r, "/settings?msg=userexists", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?msg=userok", http.StatusSeeOther)
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	co := companyID(r)
	name := strings.TrimSpace(r.PostFormValue("name"))
	orgnr := strings.TrimSpace(r.PostFormValue("orgnr"))
	if name == "" {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if err := s.Store.UpdateCompanyProfile(r.Context(), companyProfileFromForm(r, co, name, orgnr)); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/settings?msg=saved", http.StatusSeeOther)
}

func (s *Server) handleCounterparties(w http.ResponseWriter, r *http.Request) {
	cps, err := s.Store.ListCounterparties(r.Context(), companyID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	pd := s.base(r, "Kunder")
	switch r.URL.Query().Get("msg") {
	case "retention":
		pd.Error = "Kunden kan inte raderas: den har bokförda fakturor som måste sparas i sju år (bokföringslagen). Radering blockeras tills lagringstiden löpt ut."
	case "erased":
		pd.Flash = "Identitetsuppgifterna raderades."
	case "saved":
		pd.Flash = "Kunden sparades."
	}
	pd.Data = cps
	render(w, "counterparties", pd, http.StatusOK)
}

func (s *Server) handleCounterpartyEdit(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cp, err := s.Store.GetCounterparty(r.Context(), companyID(r), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if cp.Erased {
		http.Redirect(w, r, "/counterparties", http.StatusSeeOther)
		return
	}
	pd := s.base(r, "Redigera kund")
	pd.Data = cp
	render(w, "counterparty_edit", pd, http.StatusOK)
}

func (s *Server) handleCounterpartyUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	kind := r.PostFormValue("kind")
	if kind != "supplier" {
		kind = "customer"
	}
	cp := store.Counterparty{
		Kind:         kind,
		Name:         strings.TrimSpace(r.PostFormValue("name")),
		OrgNr:        strings.TrimSpace(r.PostFormValue("orgnr")),
		Personnummer: strings.TrimSpace(r.PostFormValue("personnummer")),
		Address:      strings.TrimSpace(r.PostFormValue("address")),
		IBAN:         strings.TrimSpace(r.PostFormValue("iban")),
	}
	if cp.Name == "" {
		http.Redirect(w, r, "/counterparties/"+id.String()+"/edit", http.StatusSeeOther)
		return
	}
	if err := s.Store.UpdateCounterparty(r.Context(), companyID(r), id, cp); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/counterparties?msg=saved", http.StatusSeeOther)
}

func (s *Server) handleCounterpartyErase(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch err := s.Store.EraseCounterparty(r.Context(), companyID(r), id); {
	case err == nil:
		http.Redirect(w, r, "/counterparties?msg=erased", http.StatusSeeOther)
	case errors.Is(err, store.ErrRetentionBlocked):
		http.Redirect(w, r, "/counterparties?msg=retention", http.StatusSeeOther)
	default:
		s.fail(w, err)
	}
}

func (s *Server) handleAddCounterparty(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.PostFormValue("name"))
	kind := r.PostFormValue("kind")
	if kind != "supplier" {
		kind = "customer"
	}
	if name != "" {
		if _, err := s.Store.CreateCounterparty(r.Context(), companyID(r), store.Counterparty{
			Kind:  kind,
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
	currency := strings.ToUpper(strings.TrimSpace(r.PostFormValue("currency")))
	if currency == "" {
		currency = "SEK"
	}
	ratePPM := int64(1_000_000)
	if currency != "SEK" {
		ratePPM = parseRatePPM(r.PostFormValue("rate"))
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
	if _, err := s.Store.CreateInvoice(r.Context(), companyID(r), cpID, invoice.Invoice{Currency: currency, RatePPM: ratePPM, Lines: lines}); err != nil {
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
	if _, _, err := s.Store.FinalizeInvoice(r.Context(), co, id, time.Now(), time.Now().AddDate(0, 0, 30)); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/invoices", http.StatusSeeOther)
}

func (s *Server) handleInvoiceDelete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.Store.DeleteDraftInvoice(r.Context(), companyID(r), id); err != nil && !errors.Is(err, store.ErrNotDraft) {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/invoices", http.StatusSeeOther)
}

func (s *Server) handleInvoiceCredit(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, _, err := s.Store.CreditInvoice(r.Context(), companyID(r), id); err != nil {
		if errors.Is(err, store.ErrNotFinalized) {
			http.Redirect(w, r, "/invoices", http.StatusSeeOther)
			return
		}
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

type payData struct {
	ID            string
	Number        string
	CustomerName  string
	Total         ledger.Amount
	Currency      string
	TotalSEK      ledger.Amount
	TotalSEKInput string
	BankAccounts  []ledger.Account
}

func (s *Server) handlePayForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	co, ctx := companyID(r), r.Context()
	v, err := s.Store.InvoiceForRender(ctx, co, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if v.Status != "finalized" {
		http.Redirect(w, r, "/invoices", http.StatusSeeOther)
		return
	}
	accts, _ := s.Store.ChartAccounts(ctx, co)
	var banks []ledger.Account
	for _, a := range accts {
		if strings.HasPrefix(a.Number, "19") {
			banks = append(banks, a)
		}
	}
	pd := s.base(r, "Registrera betalning")
	pd.Data = payData{
		ID: id.String(), Number: v.Number, CustomerName: v.Customer.Name,
		Total: v.Total, Currency: v.Currency, TotalSEK: v.TotalSEK,
		TotalSEKInput: oreDot(v.TotalSEK), BankAccounts: banks,
	}
	render(w, "pay", pd, http.StatusOK)
}

func (s *Server) handlePay(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	date, err := time.Parse("2006-01-02", r.PostFormValue("date"))
	if err != nil {
		http.Redirect(w, r, "/invoices/"+id.String()+"/pay", http.StatusSeeOther)
		return
	}
	account := strings.TrimSpace(r.PostFormValue("account"))
	if account == "" {
		account = "1930"
	}
	amount := parseKrOre(r.PostFormValue("amount"))
	if amount <= 0 {
		http.Redirect(w, r, "/invoices/"+id.String()+"/pay", http.StatusSeeOther)
		return
	}
	_, err = s.Store.RecordPayment(r.Context(), companyID(r), id, date, account, amount)
	switch {
	case err == nil:
		http.Redirect(w, r, "/invoices", http.StatusSeeOther)
	case errors.Is(err, store.ErrPaymentMismatch), errors.Is(err, store.ErrNotFinalized):
		http.Redirect(w, r, "/invoices/"+id.String()+"/pay", http.StatusSeeOther)
	default:
		s.fail(w, err)
	}
}

// --- supplier invoices (accounts payable) ---

func (s *Server) handleSupplierInvoices(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListSupplierInvoiceViews(r.Context(), companyID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	pd := s.base(r, "Kostnader")
	pd.Data = list
	render(w, "supplier_invoices", pd, http.StatusOK)
}

type supplierNewData struct {
	Suppliers    []store.Counterparty
	CostAccounts []ledger.Account
}

func (s *Server) handleSupplierInvoiceNew(w http.ResponseWriter, r *http.Request) {
	co, ctx := companyID(r), r.Context()
	cps, _ := s.Store.ListCounterparties(ctx, co)
	accts, _ := s.Store.ChartAccounts(ctx, co)
	var costs []ledger.Account
	for _, a := range accts {
		if a.Number != "" && a.Number[0] >= '4' && a.Number[0] <= '7' {
			costs = append(costs, a)
		}
	}
	var sups []store.Counterparty
	for _, c := range cps {
		if !c.Erased {
			sups = append(sups, c)
		}
	}
	pd := s.base(r, "Ny leverantörsfaktura")
	pd.Data = supplierNewData{Suppliers: sups, CostAccounts: costs}
	render(w, "supplier_invoice_new", pd, http.StatusOK)
}

func (s *Server) handleSupplierInvoiceCreate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	cpID, err := uuid.Parse(r.PostFormValue("counterparty"))
	if err != nil {
		http.Redirect(w, r, "/supplier-invoices/new", http.StatusSeeOther)
		return
	}
	date := formDateOr(r.PostFormValue("date"), time.Now())
	due := formDateOr(r.PostFormValue("due"), date.AddDate(0, 0, 30))
	code := moms.PurchaseCode(r.PostFormValue("vat"))
	net := parseKrOre(r.PostFormValue("net"))
	if _, err := s.Store.CreateSupplierInvoice(r.Context(), companyID(r), cpID,
		strings.TrimSpace(r.PostFormValue("supplier_number")), date, due,
		strings.TrimSpace(r.PostFormValue("account")), net, code,
		strings.TrimSpace(r.PostFormValue("description"))); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/supplier-invoices", http.StatusSeeOther)
}

func (s *Server) handleSupplierInvoiceFinalize(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.Store.FinalizeSupplierInvoice(r.Context(), companyID(r), id); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/supplier-invoices", http.StatusSeeOther)
}

func (s *Server) handleSupplierPayForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	co, ctx := companyID(r), r.Context()
	v, err := s.Store.SupplierInvoiceForView(ctx, co, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if v.Status != "finalized" {
		http.Redirect(w, r, "/supplier-invoices", http.StatusSeeOther)
		return
	}
	accts, _ := s.Store.ChartAccounts(ctx, co)
	var banks []ledger.Account
	for _, a := range accts {
		if strings.HasPrefix(a.Number, "19") {
			banks = append(banks, a)
		}
	}
	pd := s.base(r, "Betala leverantörsfaktura")
	pd.Data = payData{
		ID: id.String(), Number: v.SupplierNumber, CustomerName: v.SupplierName,
		Total: v.Payable, Currency: "SEK", TotalSEK: v.Payable,
		TotalSEKInput: oreDot(v.Payable), BankAccounts: banks,
	}
	render(w, "supplier_pay", pd, http.StatusOK)
}

func (s *Server) handleSupplierPay(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	date, err := time.Parse("2006-01-02", r.PostFormValue("date"))
	if err != nil {
		http.Redirect(w, r, "/supplier-invoices/"+id.String()+"/pay", http.StatusSeeOther)
		return
	}
	account := strings.TrimSpace(r.PostFormValue("account"))
	if account == "" {
		account = "1930"
	}
	_, err = s.Store.RecordSupplierPayment(r.Context(), companyID(r), id, date, account, parseKrOre(r.PostFormValue("amount")))
	switch {
	case err == nil:
		http.Redirect(w, r, "/supplier-invoices", http.StatusSeeOther)
	case errors.Is(err, store.ErrPaymentMismatch), errors.Is(err, store.ErrNotFinalized):
		http.Redirect(w, r, "/supplier-invoices/"+id.String()+"/pay", http.StatusSeeOther)
	default:
		s.fail(w, err)
	}
}

// formDateOr parses a YYYY-MM-DD form value, falling back to def.
func formDateOr(v string, def time.Time) time.Time {
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t
	}
	return def
}

type reportsData struct {
	Statements ledger.Statements
	Moms       moms.Declaration
	Rows       []ledger.AccountBalance
	From       string
	To         string
}

func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	co, ctx := companyID(r), r.Context()

	// Period defaults to the fiscal year to date (Jan 1 of `to`'s year -> today).
	to := formDateOr(r.URL.Query().Get("to"), time.Now())
	yearStart := time.Date(to.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	from := formDateOr(r.URL.Query().Get("from"), yearStart)

	// Resultaträkning + moms over the chosen period; balansräkning as of the end.
	periodTB, err := s.Store.TrialBalanceBetween(ctx, co, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	asOfTB, err := s.Store.TrialBalanceAsOf(ctx, co, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	// The equity "årets resultat" is the fiscal year-to-date result (year start
	// -> period end), independent of a custom from-date.
	yearTB, err := s.Store.TrialBalanceBetween(ctx, co, yearStart, to)
	if err != nil {
		s.fail(w, err)
		return
	}

	names := map[string]string{}
	if accts, err := s.Store.ChartAccounts(ctx, co); err == nil {
		for _, a := range accts {
			names[a.Number] = a.Name
		}
	}
	bal := make(map[string]ledger.Amount, len(periodTB))
	for _, r := range periodTB {
		bal[r.Account] = r.Net
	}
	pd := s.base(r, "Rapporter")
	pd.Data = reportsData{
		Statements: ledger.BuildStatementsPeriod(periodTB, asOfTB, ledger.ResultOf(yearTB), func(a string) string { return names[a] }),
		Moms:       moms.Report(bal),
		Rows:       asOfTB,
		From:       from.Format("2006-01-02"),
		To:         to.Format("2006-01-02"),
	}
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

func (s *Server) handleSIEImportForm(w http.ResponseWriter, r *http.Request) {
	pd := s.base(r, "Importera SIE")
	render(w, "sie_import", pd, http.StatusOK)
}

func (s *Server) handleSIEImport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		pd := s.base(r, "Importera SIE")
		pd.Error = "Kunde inte läsa filen (för stor eller ogiltig)."
		render(w, "sie_import", pd, http.StatusBadRequest)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		pd := s.base(r, "Importera SIE")
		pd.Error = "Välj en SIE-fil att importera."
		render(w, "sie_import", pd, http.StatusBadRequest)
		return
	}
	defer f.Close()

	exp, err := sie.Parse(f)
	if err != nil {
		pd := s.base(r, "Importera SIE")
		pd.Error = "Filen kunde inte tolkas som SIE 4."
		render(w, "sie_import", pd, http.StatusBadRequest)
		return
	}
	res, err := s.Store.ImportSIE(r.Context(), companyID(r), exp)
	if err != nil {
		slog.Error("sie import", "err", err)
		pd := s.base(r, "Importera SIE")
		pd.Error = "Importen avbröts: en verifikation balanserar inte eller ett konto saknas. Inget bokfördes."
		render(w, "sie_import", pd, http.StatusBadRequest)
		return
	}
	pd := s.base(r, "Importera SIE")
	pd.Flash = fmt.Sprintf("Importerade %d verifikat och %d konton.", res.Vouchers, res.AccountsSeeded)
	render(w, "sie_import", pd, http.StatusOK)
}

// handleCSV exports the ledger transactions as CSV (no lock-in, universal).
func (s *Server) handleCSV(w http.ResponseWriter, r *http.Request) {
	exp, err := s.Store.ExportSIE(r.Context(), companyID(r), time.Now().UTC())
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=pare-verifikat.csv")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"datum", "serie", "nummer", "konto", "debet_ore", "kredit_ore", "beskrivning"})
	for _, v := range exp.Vouchers {
		for _, l := range v.Lines {
			debit, credit := int64(0), int64(0)
			if l.Amount >= 0 {
				debit = l.Amount
			} else {
				credit = -l.Amount
			}
			_ = cw.Write([]string{
				v.Date.Format("2006-01-02"), v.Series, strconv.Itoa(v.Number), csvSafe(l.Account),
				strconv.FormatInt(debit, 10), strconv.FormatInt(credit, 10), csvSafe(v.Text),
			})
		}
	}
	cw.Flush()
}

// --- verifikat, undo, period lock, audit log ---

type verifikatData struct {
	Accounts      []ledger.Account
	Verifications []store.VerificationSummary
}

func (s *Server) handleVerifications(w http.ResponseWriter, r *http.Request) {
	co, ctx := companyID(r), r.Context()
	accts, err := s.Store.ChartAccounts(ctx, co)
	if err != nil {
		s.fail(w, err)
		return
	}
	vers, err := s.Store.ListVerificationSummaries(ctx, co)
	if err != nil {
		s.fail(w, err)
		return
	}
	pd := s.base(r, "Verifikat")
	pd.Data = verifikatData{Accounts: accts, Verifications: vers}
	render(w, "verifikat", pd, http.StatusOK)
}

func (s *Server) handlePostVerification(w http.ResponseWriter, r *http.Request) {
	co, ctx := companyID(r), r.Context()
	_ = r.ParseForm()
	series := strings.TrimSpace(r.PostFormValue("series"))
	if series == "" {
		series = "A"
	}
	date, err := time.Parse("2006-01-02", r.PostFormValue("date"))
	if err != nil {
		http.Redirect(w, r, "/verifications", http.StatusSeeOther)
		return
	}
	accounts := r.PostForm["account"]
	debits := r.PostForm["debit"]
	credits := r.PostForm["credit"]
	var lines []ledger.Line
	for i := range accounts {
		acc := strings.TrimSpace(accounts[i])
		if acc == "" {
			continue
		}
		d, c := parseKrOre(get(debits, i)), parseKrOre(get(credits, i))
		if d == 0 && c == 0 {
			continue
		}
		lines = append(lines, ledger.Line{Account: acc, Debit: d, Credit: c})
	}
	_, err = s.Store.PostVerification(ctx, co, series, date, strings.TrimSpace(r.PostFormValue("description")), lines, uuid.Nil)
	if err != nil {
		accts, _ := s.Store.ChartAccounts(ctx, co)
		vers, _ := s.Store.ListVerificationSummaries(ctx, co)
		pd := s.base(r, "Verifikat")
		pd.Error = verErrMsg(err)
		pd.Data = verifikatData{Accounts: accts, Verifications: vers}
		render(w, "verifikat", pd, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/verifications", http.StatusSeeOther)
}

func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.Store.UndoVerification(r.Context(), companyID(r), id); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/verifications", http.StatusSeeOther)
}

type loggRow struct {
	At          string
	Actor       string
	ActorDetail string
	Action      string
	Detail      string
}

type loggData struct {
	Locked        bool
	LockedThrough string
	Entries       []loggRow
	ChainOK       bool
	ChainBrokenAt int64
}

func (s *Server) handleLogg(w http.ResponseWriter, r *http.Request) {
	co, ctx := companyID(r), r.Context()
	through, locked, err := s.Store.LockedThrough(ctx, co)
	if err != nil {
		s.fail(w, err)
		return
	}
	entries, err := s.Store.ListAudit(ctx, co, 500)
	if err != nil {
		s.fail(w, err)
		return
	}
	d := loggData{Locked: locked}
	d.ChainOK, d.ChainBrokenAt, _ = s.Store.VerifyAuditChain(ctx, co)
	if locked {
		d.LockedThrough = through.Format("2006-01-02")
	}
	for _, e := range entries {
		d.Entries = append(d.Entries, loggRow{
			At:          e.At.Format("2006-01-02 15:04"),
			Actor:       e.Actor,
			ActorDetail: e.ActorDetail,
			Action:      e.Action,
			Detail:      e.Detail,
		})
	}
	pd := s.base(r, "Logg")
	pd.Data = d
	render(w, "logg", pd, http.StatusOK)
}

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	through, err := time.Parse("2006-01-02", r.PostFormValue("through"))
	if err == nil {
		if err := s.Store.LockPeriod(r.Context(), companyID(r), through, strings.TrimSpace(r.PostFormValue("reason"))); err != nil {
			s.fail(w, err)
			return
		}
	}
	http.Redirect(w, r, "/logg", http.StatusSeeOther)
}

func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	if reason == "" {
		http.Redirect(w, r, "/logg", http.StatusSeeOther)
		return
	}
	if err := s.Store.UnlockPeriod(r.Context(), companyID(r), reason); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/logg", http.StatusSeeOther)
}

func verErrMsg(err error) string {
	switch {
	case errors.Is(err, ledger.ErrUnbalanced):
		return "Verifikatet balanserar inte (debet måste vara lika med kredit)."
	case errors.Is(err, store.ErrPeriodClosed):
		return "Perioden är låst för det datumet. Bokför rättelsen i innevarande period."
	case errors.Is(err, store.ErrUnknownAccount):
		return "Ett konto finns inte i kontoplanen."
	case errors.Is(err, ledger.ErrTooFewLines):
		return "Ett verifikat behöver minst två rader."
	default:
		return "Kunde inte bokföra verifikatet."
	}
}

// --- helpers ---

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

func parseRatePPM(s string) int64 {
	f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	if err != nil || f <= 0 {
		return 1_000_000
	}
	return int64(math.Round(f * 1_000_000))
}

// csvSafe neutralizes spreadsheet formula injection: a cell beginning with a
// formula trigger is prefixed with an apostrophe so Excel/LibreOffice/Sheets
// treat it as text.
// oreDot formats öre with a '.' decimal (for <input type=number> defaults).
func oreDot(a ledger.Amount) string {
	v := int64(a)
	neg := ""
	if v < 0 {
		neg, v = "-", -v
	}
	return fmt.Sprintf("%s%d.%02d", neg, v/100, v%100)
}

func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}
