// SPDX-License-Identifier: AGPL-3.0-or-later
package sie

import (
	"bytes"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func sample() Export {
	return Export{
		CompanyName: "Bright Interaction åäö AB",
		OrgNr:       "556000-0000",
		YearStart:   day("2026-01-01"),
		YearEnd:     day("2026-12-31"),
		Generated:   day("2026-07-09"),
		Accounts: []Account{
			{"1930", "Företagskonto/checkkonto/affärskonto"},
			{"3001", "Försäljning inom Sverige, 25 % moms"},
			{"2611", "Utgående moms på försäljning inom Sverige, 25 %"},
			{"5010", "Lokalhyra"},
			{"2640", "Ingående moms"},
		},
		Vouchers: []Voucher{
			{Series: "A", Number: 1, Date: day("2026-01-15"), Text: "Konsultarvode", Lines: []Line{
				{Account: "1930", Amount: 1250000},
				{Account: "3001", Amount: -1000000},
				{Account: "2611", Amount: -250000},
			}},
			{Series: "A", Number: 2, Date: day("2026-01-20"), Text: "Lokalhyra", Lines: []Line{
				{Account: "5010", Amount: 200000},
				{Account: "2640", Amount: 50000},
				{Account: "1930", Amount: -250000},
			}},
		},
	}
}

func TestWriteFormatAndEncoding(t *testing.T) {
	var buf bytes.Buffer
	if err := sample().Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw := buf.Bytes()

	if !bytes.HasPrefix(raw, []byte("#FLAGGA 0\n")) {
		t.Fatal("#FLAGGA 0 must be the first line")
	}
	for _, must := range []string{"#FORMAT PC8", "#SIETYP 4", "#ORGNR 556000-0000", "#RAR 0 20260101 20261231", "#VER A 1 20260115", "#TRANS 1930 {} 12500.00", "#TRANS 3001 {} -10000.00"} {
		if !bytes.Contains(raw, []byte(must)) {
			t.Errorf("missing record: %q", must)
		}
	}
	// CP437: å=0x86 ä=0x84 ö=0x94 must appear as single bytes...
	for _, b := range []byte{0x86, 0x84, 0x94} {
		if !bytes.Contains(raw, []byte{b}) {
			t.Errorf("missing CP437 byte 0x%02x", b)
		}
	}
	// ...and the UTF-8 encoding of å (0xC3 0xA5) must NOT appear.
	if bytes.Contains(raw, []byte{0xc3, 0xa5}) {
		t.Error("output is UTF-8, not CP437")
	}
}

func TestRoundTrip(t *testing.T) {
	in := sample()
	var buf bytes.Buffer
	if err := in.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := Parse(&buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.CompanyName != in.CompanyName {
		t.Fatalf("company name round trip: %q != %q", out.CompanyName, in.CompanyName)
	}
	if out.OrgNr != in.OrgNr {
		t.Fatalf("orgnr: %q", out.OrgNr)
	}
	if len(out.Vouchers) != len(in.Vouchers) {
		t.Fatalf("voucher count %d != %d", len(out.Vouchers), len(in.Vouchers))
	}
	if err := out.Balances(); err != nil {
		t.Fatalf("parsed vouchers unbalanced: %v", err)
	}
	for vi, v := range in.Vouchers {
		ov := out.Vouchers[vi]
		if ov.Series != v.Series || ov.Number != v.Number || !ov.Date.Equal(v.Date) {
			t.Fatalf("voucher header mismatch: %+v vs %+v", ov, v)
		}
		for li, l := range v.Lines {
			if ov.Lines[li].Account != l.Account || ov.Lines[li].Amount != l.Amount {
				t.Fatalf("line %d/%d mismatch: %+v vs %+v", vi, li, ov.Lines[li], l)
			}
		}
	}
}

func TestBalancesRejectsUnbalanced(t *testing.T) {
	e := Export{Vouchers: []Voucher{{Series: "A", Number: 9, Lines: []Line{
		{Account: "1930", Amount: 100},
		{Account: "3001", Amount: -99},
	}}}}
	if err := e.Balances(); err == nil {
		t.Fatal("unbalanced voucher accepted")
	}
	if err := e.Write(&bytes.Buffer{}); err == nil {
		t.Fatal("Write accepted an unbalanced voucher")
	}
}
