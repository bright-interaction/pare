// SPDX-License-Identifier: AGPL-3.0-or-later
package render

import (
	"strings"
	"testing"
)

// The faktura HTML must carry the mandatory content: seller VAT number + address,
// a per-rate VAT breakout, the reverse-charge legal reference, and a payee so the
// invoice can be paid.
func TestInvoiceHTMLCompliance(t *testing.T) {
	inv := Invoice{
		Number: "2026-0001", Date: "2026-02-01", DueDate: "2026-03-03",
		CompanyName: "Bright Interaction AB", CompanyOrgNr: "556000-0000",
		CompanyMomsRegNr: "SE556000000001", CompanyAddress: "Storgatan 1",
		CompanyPostal: "111 22 Stockholm", CompanyFSkatt: true,
		Bankgiro: "123-4567", IBAN: "SE1234",
		CustomerName: "EU Kund GmbH", CustomerOrgNr: "DE123456789",
		Currency: "SEK",
		Lines: []InvoiceLine{
			{Description: "Konsult", Quantity: "10", UnitPrice: "1 000,00", VATLabel: "25 %", Net: "10 000,00"},
			{Description: "EU-tjänst", Quantity: "1", UnitPrice: "5 000,00", VATLabel: "Omvänd", Net: "5 000,00"},
		},
		VATSummary: []VATRow{
			{Label: "Moms 25 %", NetKr: "10 000,00", VATKr: "2 500,00"},
			{Label: "Omvänd skattskyldighet", NetKr: "5 000,00", VATKr: "0,00"},
		},
		LegalNotes: []string{"Omvänd betalningsskyldighet. Reverse charge. Köparen redovisar moms (artikel 196, mervärdesskattedirektivet 2006/112/EG)."},
		NetKr:      "15 000,00", VATKr: "2 500,00", TotalKr: "17 500,00",
	}
	html, err := InvoiceHTML(inv)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"SE556000000001",         // seller VAT number
		"Storgatan 1",            // seller address
		"Godkänd för F-skatt",    // F-skatt line
		"Moms 25 % på",           // per-rate breakout
		"Omvänd skattskyldighet", // reverse-charge summary row
		"Reverse charge",         // legal note
		"Bankgiro",               // payee
		"123-4567",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("faktura HTML missing %q", want)
		}
	}
}
