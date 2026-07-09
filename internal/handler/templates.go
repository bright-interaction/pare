// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package handler

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed templates/*.html
var tmplFS embed.FS

var funcs = template.FuncMap{
	"seq": func(n int) []int {
		s := make([]int, n)
		for i := range s {
			s[i] = i
		}
		return s
	},
}

var pages = func() map[string]*template.Template {
	m := map[string]*template.Template{}
	for _, p := range []string{"setup", "login", "dashboard", "counterparties", "counterparty_edit", "invoices", "invoice_new", "pay", "supplier_invoices", "supplier_invoice_new", "supplier_pay", "verifikat", "reports", "logg", "settings", "sie_import"} {
		m[p] = template.Must(template.New("").Funcs(funcs).ParseFS(tmplFS, "templates/layout.html", "templates/"+p+".html"))
	}
	return m
}()

// pageData is the common template model.
type pageData struct {
	Title       string
	Email       string
	CompanyName string
	Flash       string
	Error       string
	CSRF        string
	Data        any
}

func render(w http.ResponseWriter, page string, d pageData, status int) {
	t, ok := pages[page]
	if !ok {
		http.Error(w, "unknown page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = t.ExecuteTemplate(w, "layout", d)
}
