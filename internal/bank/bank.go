// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

// Package bank parses bank statements (ISO 20022 camt.053 and a simple CSV) into
// normalized entries for reconciliation. It is a pure parsing package: no I/O,
// no DB. The same normalized Entry is what a live PSD2 feed would produce, so the
// reconciliation engine is source-agnostic.
package bank

import (
	"strings"
	"time"
)

// Entry is one normalized bank transaction. AmountOre is signed: credit (money
// in) positive, debit (money out) negative.
type Entry struct {
	Date      time.Time
	AmountOre int64
	Text      string // remittance info / counterparty, for matching + display
	Ref       string // structured reference / OCR, if present
}

// parseAmountOre parses a decimal amount in öre, tolerant of both dot-decimal
// (camt: "1234.56") and Swedish comma-decimal with space/dot thousands
// ("1 234,56", "1.234,56"), and parenthesised or minus-signed negatives.
func parseAmountOre(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	if strings.HasPrefix(s, "-") {
		neg, s = true, strings.TrimPrefix(s, "-")
	}
	s = strings.TrimPrefix(s, "+")
	// Drop spaces (incl. non-breaking) and any currency letters.
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' {
			b.WriteRune(r)
		}
	}
	s = b.String()
	if s == "" {
		return 0, false
	}
	// Decide the decimal separator: whichever of '.' / ',' appears last is the
	// decimal; the other is a thousands separator and is stripped.
	lastDot, lastComma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	dec := "."
	if lastComma > lastDot {
		dec = ","
	}
	other := "."
	if dec == "." {
		other = ","
	}
	s = strings.ReplaceAll(s, other, "")
	whole, frac, _ := strings.Cut(s, dec)
	var ore int64
	for _, r := range whole {
		ore = ore*10 + int64(r-'0')
	}
	ore *= 100
	frac = (frac + "00")[:2]
	ore += int64(frac[0]-'0')*10 + int64(frac[1]-'0')
	if neg {
		ore = -ore
	}
	return ore, true
}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if len(s) >= 10 {
		s = s[:10]
	}
	for _, layout := range []string{"2006-01-02", "2006/01/02", "02.01.2006", "02/01/2006", "20060102"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
