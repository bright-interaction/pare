// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
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

// TestParseAmountSwedish covers the CSV locale contract: ',' is the decimal
// comma, '.' and (non-breaking) space group thousands. The grouped cases are the
// 2026-07-28 HIGH-1 regression: they used to import 1000x to 1000000x too small
// with ok=true.
func TestParseAmountSwedish(t *testing.T) {
	cases := map[string]int64{
		// Grouped thousands, the reported bug. "12.500" is the modal Swedish
		// amount and was 1250 öre (12,50 kr) before the fix.
		"12.500":        1250000,
		"1.500.000":     150000000,
		"999.500":       99950000,
		"1.234.567":     123456700,
		"1,234,567":     123456700,
		"1.234":         123400,
		"(1.500.000)":   -150000000,
		"-12.500":       -1250000,
		"1.500.000 SEK": 150000000,
		// Grouping plus a decimal part.
		"1.234,56":     123456,
		"1.000.000,00": 100000000,
		"1,234.56":     123456,
		// Plain decimals: the separator is not in grouped shape, so it stays the
		// decimal separator.
		"1234.56":  123456,
		"12,50":    1250,
		"1,5":      150,
		"0.500":    50, // leading-zero first group is 0,50 kr, not 500 kr
		"1234.500": 123450,
		"-500,00":  -50000,
		"(500.00)": -50000,
		"42":       4200,
		"0":        0,
		// Space grouping, in the three forms a bank actually emits.
		"1 234,56":  123456,
		"12 500,00": 1250000,
		"1 234,56":  123456, // non-breaking space
		"1 234,56":  123456, // narrow non-breaking space
		"1 234,56-": -123456,
		// A currency token must not swallow the sign: "SEK -1234,00" is money out,
		// and reading it as money in flips a debit into a credit.
		"SEK -1234,00": -123400,
		"-1234,00 kr":  -123400,
		"SEK 1234,00":  123400,
		// The largest representable amount, one öre under the int64 boundary.
		"92233720368547757,99": 9223372036854775799,
	}
	for in, want := range cases {
		if got, ok := parseAmountSwedish(in); !ok || got != want {
			t.Errorf("parseAmountSwedish(%q) = %d ok=%v, want %d ok=true", in, got, ok, want)
		}
	}
}

// TestParseAmountSwedishRejects: input that is not unambiguously a number must
// return ok=false so ParseCSV skips the row. Fabricating a plausible amount from
// junk is the failure mode HIGH-1 is about, so a fail-open answer here is a bug
// even when the number it invents looks reasonable.
func TestParseAmountSwedishRejects(t *testing.T) {
	for _, in := range []string{
		"1..5", "..", ",", ".", "1.", ",50", "1..", "..1",
		"1.2.3", "3.14.159", "1.5.000",
		"1.500.00",     // last group is 2 digits: neither grouping nor a decimal
		"1.500.000.00", // same, one group deeper
		"05.03.2026",   // a dotted date in the amount column
		"31.12.2025",
		"1.234,56,78", // two decimal commas
		"1,23.456",    // grouping groups are not 3 digits
		// A single comma with exactly three digits after it is the one shape that
		// is both a Swedish 3-decimal amount and a foreign thousands group. It is
		// refused rather than guessed at in either direction.
		"1,234", "12,500", "999,500", "-1,500", "(1,500)",
		"1O5", // letter O, not a zero
		"12,3a4",
		"abc", "", "   ", "kr",
		"500)", "(500", "1-2", "-", "()", "--5", // stray sign characters
		// Overflow: must be rejected, never wrapped to a negative amount.
		"184467440737095516",            // 18 digits: *100 overflows int64
		"9223372036854775808",           // MaxInt64+1
		"99999999999999999999,99",       // 20 digits: overflows during accumulation
		"92233720368547758.99",          // the exact boundary the old guard missed
		"92.233.720.368.547.758,99",     // same value, grouped
		"1.000.000.000.000.000.000.000", // 1e21 kr
	} {
		if got, ok := parseAmountSwedish(in); ok {
			t.Errorf("parseAmountSwedish(%q) = %d ok=true, want ok=false", in, got)
		}
	}
}

// TestParseAmountISO covers the camt.053 contract: '.' is always the decimal
// point, grouping is illegal. This is the half of the old shared helper that was
// already right, locked so a future locale tweak cannot break camt.
func TestParseAmountISO(t *testing.T) {
	cases := map[string]int64{
		"1234.56": 123456, "12500.00": 1250000, "0.50": 50, "0.500": 50,
		"42": 4200, "-2500.00": -250000, "0000123.45": 12345,
		"1234,56":              123456, // a comma that cannot be a group is the decimal
		"92233720368547757.99": 9223372036854775799,
	}
	for in, want := range cases {
		if got, ok := parseAmountISO(in); !ok || got != want {
			t.Errorf("parseAmountISO(%q) = %d ok=%v, want %d ok=true", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"1.500.000", "1,234.56", "1.234,56", "1..5", "1.", ".5", "",
		"92233720368547758.99", "9223372036854775808",
		// Also a valid group, so a non-conformant exporter could mean 1 234. Not
		// guessed at, in a format where a 1000x error is the alternative.
		"1,234", "12,500",
	} {
		if got, ok := parseAmountISO(in); ok {
			t.Errorf("parseAmountISO(%q) = %d ok=true, want ok=false", in, got)
		}
	}
}

// TestParseCSVGroupedThousands is the end-to-end HIGH-1 repro through the real
// import path: a Swedish statement whose amounts use dot grouping. Before the
// fix these imported as 1250, 150 and 99950 öre.
func TestParseCSVGroupedThousands(t *testing.T) {
	csv := "Datum;Belopp;Text\n" +
		"2026-03-05;12.500;Hyra\n" +
		"2026-03-06;1.500.000;Stor faktura\n" +
		"2026-03-07;999.500;Mellan\n" +
		"2026-03-08;12 500,00;Space-grupperad\n"
	es, err := ParseCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []int64{1250000, 150000000, 99950000, 1250000}
	if len(es) != len(want) {
		t.Fatalf("want %d entries, got %d: %+v", len(want), len(es), es)
	}
	for i, w := range want {
		if es[i].AmountOre != w {
			t.Errorf("row %d amount = %d, want %d", i, es[i].AmountOre, w)
		}
	}
}

// TestParseCSVRejectsDateInAmountColumn: when the header matches no keyword
// ParseCSV falls back to positional date,amount,text, and a Swedish export with
// two date columns then feeds a dotted date to the amount parser. That must be
// rejected (row skipped), never imported as a multi-million-krona phantom.
func TestParseCSVRejectsDateInAmountColumn(t *testing.T) {
	csv := "Reskontradag;Valutadag;Belopp;Text\n05.03.2026;06.03.2026;1.234,50;Overforing\n"
	es, err := ParseCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(es) != 0 {
		t.Fatalf("want 0 entries (amount column holds a date), got %d: %+v", len(es), es)
	}
}
