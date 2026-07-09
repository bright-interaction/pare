// SPDX-License-Identifier: AGPL-3.0-or-later
package handler

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brightinteraction/pare/internal/ledger"
	"github.com/brightinteraction/pare/internal/moms"
	"github.com/brightinteraction/pare/internal/store"
)

// renderOK executes a page template and fails on a non-200 or any template
// execution error surfacing in the body.
func renderOK(t *testing.T, page string, pd pageData, want ...string) {
	t.Helper()
	rec := httptest.NewRecorder()
	render(rec, page, pd, 200)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%s: status %d", page, rec.Code)
	}
	if strings.Contains(body, "template:") || strings.Contains(body, "<no value>") {
		t.Fatalf("%s: template execution error: %s", page, body)
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("%s: missing %q", page, w)
		}
	}
}

func TestFormTemplatesRender(t *testing.T) {
	renderOK(t, "pay", pageData{Title: "Betala", Email: "op@x.se", CSRF: "tok", Data: payData{
		ID: "abc", Number: "2026-0001", CustomerName: "Kund AB",
		Total: ledger.SEK(1250, 0), Currency: "EUR", TotalSEK: ledger.SEK(13750, 0),
		TotalSEKInput: "13750.00",
		BankAccounts:  []ledger.Account{{Number: "1930", Name: "Företagskonto"}},
	}}, "Registrera betalning", `name="csrf" value="tok"`, "2026-0001")

	renderOK(t, "counterparty_edit", pageData{Title: "Redigera", Email: "op@x.se", CSRF: "tok", Data: store.Counterparty{
		Kind: "supplier", Name: "Leverantör AB", OrgNr: "556000-0000",
		Personnummer: "", Address: "Gatan 1", IBAN: "SE35",
	}}, "Radera personuppgifter", `name="csrf" value="tok"`, "Leverantör AB")

	renderOK(t, "settings", pageData{Title: "Företag", Email: "op@x.se", CSRF: "tok", Data: store.CompanyInfo{
		Name: "BI AB", OrgNr: "556000-0000", MomsRegNr: "SE556000000001", Bankgiro: "123-4567", FSkatt: true,
	}}, "Företagsuppgifter", `value="SE556000000001"`)

	renderOK(t, "supplier_invoices", pageData{Title: "Kostnader", Email: "op@x.se", CSRF: "tok", Data: []store.SupplierInvoiceView{
		{ID: [16]byte{1}, SupplierName: "Anthropic PBC", SupplierNumber: "INV-1", VATLabel: "Tjänst utanför EU, omvänd moms", Payable: ledger.SEK(10000, 0), Status: "finalized"},
	}}, "Kostnader", "Anthropic PBC", "Betala")

	renderOK(t, "supplier_invoice_new", pageData{Title: "Ny", Email: "op@x.se", CSRF: "tok", Data: supplierNewData{
		Suppliers:    []store.Counterparty{{Name: "Anthropic PBC"}},
		CostAccounts: []ledger.Account{{Number: "6540", Name: "IT-tjänster"}},
	}}, "Momsbehandling", "omvänd moms")

	renderOK(t, "supplier_pay", pageData{Title: "Betala", Email: "op@x.se", CSRF: "tok", Data: payData{
		ID: "abc", Number: "INV-1", CustomerName: "Anthropic PBC", Total: ledger.SEK(10000, 0),
		Currency: "SEK", TotalSEKInput: "10000.00", BankAccounts: []ledger.Account{{Number: "1930", Name: "Bank"}},
	}}, "Betala leverantörsfaktura", `value="tok"`)

	renderOK(t, "sie_import", pageData{Title: "Importera SIE", Email: "op@x.se", CSRF: "tok"}, "Importera SIE", `name="csrf" value="tok"`)

	renderOK(t, "bokslut", pageData{Title: "Bokslut", Email: "op@x.se", CSRF: "tok", Data: []store.FiscalYear{
		{Label: "2026", StartsOn: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndsOn: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), Closed: false},
	}}, "räkenskapsår", "Stäng år", "2026")
}

// The reports template references many nested Statements/Declaration fields;
// template.Must only checks parse, not field access, so execute it for real and
// assert it renders the statement sections without a template error.
func TestReportsTemplateRenders(t *testing.T) {
	tb := []ledger.AccountBalance{
		{Account: "1930", Class: ledger.ClassAsset, Net: ledger.SEK(10030, 0)},
		{Account: "2611", Class: ledger.ClassEquityLiability, Net: -ledger.SEK(2500, 0)},
		{Account: "2640", Class: ledger.ClassEquityLiability, Net: ledger.SEK(500, 0)},
		{Account: "3001", Class: ledger.ClassIncome, Net: -ledger.SEK(10000, 0)},
		{Account: "5010", Class: ledger.ClassExpense, Net: ledger.SEK(2000, 0)},
		{Account: "8310", Class: ledger.ClassFinancial, Net: -ledger.SEK(30, 0)},
	}
	bal := map[string]ledger.Amount{}
	for _, r := range tb {
		bal[r.Account] = r.Net
	}
	pd := pageData{Title: "Rapporter", Email: "op@example.com", Data: reportsData{
		Statements: ledger.BuildStatements(tb, func(a string) string { return "Konto " + a }),
		Moms:       moms.Report(bal),
		Rows:       tb,
	}}

	rec := httptest.NewRecorder()
	render(rec, "reports", pd, 200)

	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"Resultaträkning", "Balansräkning", "Årets resultat", "Momsdeklaration", "Summa eget kapital och skulder"} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered report missing %q", want)
		}
	}
	// A template execution error writes the error text mid-stream; guard against it.
	if strings.Contains(body, "template:") || strings.Contains(body, "<no value>") {
		t.Fatalf("template execution error in output: %s", body)
	}
}
