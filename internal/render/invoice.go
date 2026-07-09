// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package render

import (
	"bytes"
	"context"
	"html/template"
)

// Invoice is the pre-formatted data the PDF template renders. The store builds
// it (amounts already formatted as Swedish kronor strings) so this package
// stays free of ledger/store imports.
type Invoice struct {
	Number           string
	Date             string
	DueDate          string
	CompanyName      string
	CompanyOrgNr     string
	CompanyMomsRegNr string
	CompanyAddress   string
	CompanyPostal    string
	CompanyFSkatt    bool
	Bankgiro         string
	IBAN             string
	CustomerName     string
	CustomerOrgNr    string
	CustomerAddress  string
	OCR              string
	Lines            []InvoiceLine
	VATSummary       []VATRow
	LegalNotes       []string
	NetKr            string
	VATKr            string
	TotalKr          string
	Currency         string
}

// InvoiceLine is one pre-formatted row.
type InvoiceLine struct {
	Description string
	Quantity    string
	UnitPrice   string
	VATLabel    string
	Net         string
}

// VATRow is one pre-formatted per-rate VAT breakout row.
type VATRow struct {
	Label string
	NetKr string
	VATKr string
}

var invoiceTmpl = template.Must(template.New("invoice").Parse(`<!doctype html>
<html lang="sv"><head><meta charset="utf-8"><style>
  * { box-sizing: border-box; }
  body { font-family: -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
         color: #1a1a2e; font-size: 13px; line-height: 1.5; margin: 0; }
  .head { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 36px; }
  .title { font-size: 30px; font-weight: 700; letter-spacing: -0.5px; margin: 0 0 2px; }
  .muted { color: #6b7280; }
  .parties { display: flex; justify-content: space-between; gap: 40px; margin-bottom: 28px; }
  .parties h3 { font-size: 11px; text-transform: uppercase; letter-spacing: 0.6px; color: #6b7280; margin: 0 0 6px; }
  .meta td { padding: 2px 0; }
  .meta td:first-child { color: #6b7280; padding-right: 18px; }
  table.lines { width: 100%; border-collapse: collapse; margin: 8px 0 20px; }
  table.lines th { text-align: left; font-size: 11px; text-transform: uppercase; letter-spacing: 0.5px;
                   color: #6b7280; border-bottom: 2px solid #1a1a2e; padding: 7px 8px; }
  table.lines td { padding: 8px; border-bottom: 1px solid #e5e7eb; }
  .num { text-align: right; font-variant-numeric: tabular-nums; }
  .totals { width: 260px; margin-left: auto; }
  .totals td { padding: 4px 8px; }
  .totals .grand td { border-top: 2px solid #1a1a2e; font-weight: 700; font-size: 15px; padding-top: 8px; }
  .pay { margin-top: 34px; padding: 14px 16px; background: #f6f7fb; border-radius: 8px; }
  .pay strong { display: inline-block; min-width: 130px; color: #6b7280; font-weight: 500; }
  .notes { margin-top: 18px; font-size: 12px; color: #374151; }
  .notes div { margin-bottom: 3px; }
</style></head><body>
  <div class="head">
    <div>
      <p class="title">Faktura</p>
      <div class="muted">Nr {{.Number}}</div>
    </div>
    <table class="meta">
      <tr><td>Fakturadatum</td><td>{{.Date}}</td></tr>
      <tr><td>Förfallodatum</td><td>{{.DueDate}}</td></tr>
    </table>
  </div>

  <div class="parties">
    <div>
      <h3>Säljare</h3>
      <div><strong>{{.CompanyName}}</strong></div>
      {{if .CompanyAddress}}<div class="muted">{{.CompanyAddress}}</div>{{end}}
      {{if .CompanyPostal}}<div class="muted">{{.CompanyPostal}}</div>{{end}}
      {{if .CompanyOrgNr}}<div class="muted">Org.nr {{.CompanyOrgNr}}</div>{{end}}
      {{if .CompanyMomsRegNr}}<div class="muted">Momsreg.nr {{.CompanyMomsRegNr}}</div>{{end}}
      {{if .CompanyFSkatt}}<div class="muted">Godkänd för F-skatt</div>{{end}}
    </div>
    <div>
      <h3>Köpare</h3>
      <div><strong>{{.CustomerName}}</strong></div>
      {{if .CustomerOrgNr}}<div class="muted">Org.nr {{.CustomerOrgNr}}</div>{{end}}
      {{if .CustomerAddress}}<div class="muted">{{.CustomerAddress}}</div>{{end}}
    </div>
  </div>

  <table class="lines">
    <thead><tr>
      <th>Beskrivning</th><th class="num">Antal</th><th class="num">À-pris</th>
      <th class="num">Moms</th><th class="num">Belopp</th>
    </tr></thead>
    <tbody>
    {{range .Lines}}<tr>
      <td>{{.Description}}</td>
      <td class="num">{{.Quantity}}</td>
      <td class="num">{{.UnitPrice}}</td>
      <td class="num">{{.VATLabel}}</td>
      <td class="num">{{.Net}}</td>
    </tr>{{end}}
    </tbody>
  </table>

  <table class="totals">
    <tr><td>Netto</td><td class="num">{{.NetKr}} {{.Currency}}</td></tr>
    {{range .VATSummary}}<tr><td>{{.Label}} på {{.NetKr}}</td><td class="num">{{.VATKr}} {{$.Currency}}</td></tr>{{end}}
    <tr class="grand"><td>Att betala</td><td class="num">{{.TotalKr}} {{.Currency}}</td></tr>
  </table>

  {{if .LegalNotes}}<div class="notes">{{range .LegalNotes}}<div>{{.}}</div>{{end}}</div>{{end}}

  <div class="pay">
    <div><strong>Att betala</strong>{{.TotalKr}} {{.Currency}}</div>
    <div><strong>Förfallodatum</strong>{{.DueDate}}</div>
    {{if .Bankgiro}}<div><strong>Bankgiro</strong>{{.Bankgiro}}</div>{{end}}
    {{if .IBAN}}<div><strong>IBAN</strong>{{.IBAN}}</div>{{end}}
    {{if .OCR}}<div><strong>OCR / referens</strong>{{.OCR}}</div>{{end}}
  </div>
</body></html>`))

// InvoiceHTML renders the invoice to an HTML string.
func InvoiceHTML(inv Invoice) (string, error) {
	var b bytes.Buffer
	if err := invoiceTmpl.Execute(&b, inv); err != nil {
		return "", err
	}
	return b.String(), nil
}

// RenderInvoicePDF renders the invoice HTML and converts it to PDF.
func RenderInvoicePDF(ctx context.Context, g *Gotenberg, inv Invoice) ([]byte, error) {
	html, err := InvoiceHTML(inv)
	if err != nil {
		return nil, err
	}
	return g.HTMLToPDF(ctx, html)
}
