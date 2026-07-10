// SPDX-License-Identifier: AGPL-3.0-or-later
package bank

import (
	"strings"
	"testing"
)

const sampleCAMT = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">
 <BkToCstmrStmt><Stmt>
  <Ntry>
    <Amt Ccy="SEK">12500.00</Amt><CdtDbtInd>CRDT</CdtDbtInd>
    <BookgDt><Dt>2026-03-05</Dt></BookgDt>
    <NtryDtls><TxDtls>
      <RmtInf><Ustrd>Betalning faktura 2026-0001</Ustrd></RmtInf>
      <RltdPties><Dbtr><Nm>Kund AB</Nm></Dbtr></RltdPties>
    </TxDtls></NtryDtls>
  </Ntry>
  <Ntry>
    <Amt Ccy="SEK">2500.00</Amt><CdtDbtInd>DBIT</CdtDbtInd>
    <BookgDt><Dt>2026-03-06</Dt></BookgDt>
    <AddtlNtryInf>Kortkop Anthropic</AddtlNtryInf>
  </Ntry>
 </Stmt></BkToCstmrStmt>
</Document>`

func TestParseCAMT(t *testing.T) {
	es, err := ParseCAMT(strings.NewReader(sampleCAMT))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(es) != 2 {
		t.Fatalf("want 2 entries, got %d", len(es))
	}
	if es[0].AmountOre != 1250000 || es[0].Date.Format("2006-01-02") != "2026-03-05" {
		t.Fatalf("credit entry wrong: %+v", es[0])
	}
	if !strings.Contains(es[0].Text, "2026-0001") {
		t.Fatalf("remittance text lost: %q", es[0].Text)
	}
	if es[1].AmountOre != -250000 { // DBIT -> negative
		t.Fatalf("debit sign wrong: %d", es[1].AmountOre)
	}
}

func TestParseCSVSwedish(t *testing.T) {
	csv := "Datum;Belopp;Text\n2026-03-05;12 500,00;Faktura 2026-0001\n2026-03-06;-2 500,00;Anthropic\n"
	es, err := ParseCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(es) != 2 {
		t.Fatalf("want 2, got %d", len(es))
	}
	if es[0].AmountOre != 1250000 || es[1].AmountOre != -250000 {
		t.Fatalf("amounts wrong: %+v", es)
	}
}

func TestParseAmountOre(t *testing.T) {
	cases := map[string]int64{
		"1234.56": 123456, "1 234,56": 123456, "1.234,56": 123456,
		"1,234.56": 123456, "-500,00": -50000, "(500.00)": -50000, "42": 4200,
	}
	for in, want := range cases {
		if got, ok := parseAmountOre(in); !ok || got != want {
			t.Errorf("parseAmountOre(%q) = %d ok=%v, want %d", in, got, ok, want)
		}
	}
}
