// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

// Package bank parses bank statements (ISO 20022 camt.053 and a simple CSV) into
// normalized entries for reconciliation. It is a pure parsing package: no I/O,
// no DB. The same normalized Entry is what a live PSD2 feed would produce, so the
// reconciliation engine is source-agnostic.
package bank

import (
	"math"
	"strings"
	"time"
	"unicode"
)

// Entry is one normalized bank transaction. AmountOre is signed: credit (money
// in) positive, debit (money out) negative.
type Entry struct {
	Date      time.Time
	AmountOre int64
	Text      string // remittance info / counterparty, for matching + display
	Ref       string // structured reference / OCR, if present
}

// Amount parsing is split per source format on purpose, because the two callers
// disagree about what '.' means and no single helper can serve both:
//
//   - camt.053 (ParseCAMT) is ISO 20022. <Amt> is a plain decimal number: '.' is
//     always the decimal point and a thousands separator is illegal.
//   - A Swedish bank CSV (ParseCSV) is locale-formatted: ',' is the decimal
//     comma and '.' or a (non-breaking) space groups thousands, so "1.500.000"
//     is 1 500 000 kr and "12.500" is 12 500 kr.
//
// The one shared helper this replaced picked the last separator as the decimal
// and cut on the first one, so every dot-grouped CSV amount imported 1000x to
// 1000000x too small, silently, with ok=true (audit 2026-07-28, HIGH-1).
//
// Both parsers are fail-closed: input that is not unambiguously a number returns
// ok=false and the caller skips the row, rather than importing a fabricated
// amount. Skipping is also how ParseCSV discards its own header line.

// parseAmountISO parses an ISO 20022 amount (camt.053 <Amt>) into öre. At most
// one separator is allowed and it is the decimal point. A ',' is tolerated in
// that position because grouping is illegal in this format, with one exception:
// "1,234" also reads as a group under a non-conformant exporter, so that exact
// shape is refused rather than guessed at (guessing wrong there is a 1000x
// error). Two or more separators mean the document is not camt and are rejected.
// More than two fraction digits are truncated, which is what the previous parser
// did for the currencies that allow them.
func parseAmountISO(s string) (int64, bool) {
	tok, neg, ok := normalizeAmount(s)
	if !ok {
		return 0, false
	}
	if strings.Count(tok, ".")+strings.Count(tok, ",") > 1 {
		return 0, false
	}
	if strings.Contains(tok, ",") && isGrouped(tok, ',') {
		return 0, false
	}
	whole, frac := tok, ""
	if i := strings.IndexAny(tok, ".,"); i >= 0 {
		whole, frac = tok[:i], tok[i+1:]
	}
	return oreFromParts(whole, frac, neg)
}

// parseAmountSwedish parses one amount cell from a bank CSV into öre. Grouping
// is validated rather than guessed: a separator is only read as a thousands
// separator when the token has the shape of a grouped integer (see isGrouped),
// which is what tells "12.500" (12 500 kr) apart from "1234.56" (1234,56 kr),
// from a dotted date "05.03.2026" and from junk like "1..5". Handles space and
// non-breaking-space grouping, parenthesised and signed negatives, and a
// trailing currency token.
//
// The one shape no rule can settle from the token alone is a single comma with
// exactly three digits after it ("1,234"), because that is both a Swedish
// three-decimal amount and a foreign thousands group. It returns ok=false and
// the row is skipped, so the operator sees a missing line instead of an amount
// that is silently 1000x off in an unknown direction.
func parseAmountSwedish(s string) (int64, bool) {
	tok, neg, ok := normalizeAmount(s)
	if !ok {
		return 0, false
	}
	dots, commas := strings.Count(tok, "."), strings.Count(tok, ",")
	switch {
	case dots == 0 && commas == 0:
		return oreFromParts(tok, "", neg)

	case dots > 0 && commas > 0:
		// Both present: the one that appears last is the decimal separator, the
		// other must be valid grouping over the whole part.
		dec, grp := ",", "."
		if strings.LastIndex(tok, ".") > strings.LastIndex(tok, ",") {
			dec, grp = ".", ","
		}
		if strings.Count(tok, dec) != 1 {
			return 0, false // two decimal points is not a number
		}
		whole, frac, _ := strings.Cut(tok, dec)
		if !isGrouped(whole, grp[0]) {
			return 0, false
		}
		return oreFromParts(strings.ReplaceAll(whole, grp, ""), frac, neg)

	case commas == 1:
		// ',' is the Swedish decimal comma, so a single one is the decimal point,
		// EXCEPT for the one shape that is also a valid group ("1,234"): in a
		// Swedish file that is 1,234 kr and in a foreign one it is 1 234 kr, a
		// 1000x spread either way, so it is refused instead of guessed at. Write
		// it as "1,23" or "1.234", or export camt.053.
		if isGrouped(tok, ',') {
			return 0, false
		}
		whole, frac, _ := strings.Cut(tok, ",")
		return oreFromParts(whole, frac, neg)

	case dots == 1:
		// '.' groups thousands in this locale, so the grouped shape wins ("12.500"
		// is 12 500 kr). Anything else is a plain dot-decimal, which some exports
		// emit ("1234.56").
		if isGrouped(tok, '.') {
			return oreFromParts(strings.ReplaceAll(tok, ".", ""), "", neg)
		}
		whole, frac, _ := strings.Cut(tok, ".")
		return oreFromParts(whole, frac, neg)

	default:
		// Two or more of the same separator can only be grouping; a number never
		// has two decimal points.
		sep := "."
		if commas > 1 {
			sep = ","
		}
		if !isGrouped(tok, sep[0]) {
			return 0, false
		}
		return oreFromParts(strings.ReplaceAll(tok, sep, ""), "", neg)
	}
}

// isGrouped reports whether s is a thousands-grouped integer under sep, e.g.
// "1.234.567" or "12.500". Every group after the first must be exactly three
// digits and the first group one to three digits with no leading zero. That is
// the whole disambiguation: "1234.56" (first group too long) and "0.500" (a
// leading-zero first group, i.e. 0,50 kr) are decimals, while "05.03.2026" and
// "1.500.00" are neither and get rejected instead of imported.
func isGrouped(s string, sep byte) bool {
	parts := strings.Split(s, string(sep))
	if len(parts) < 2 {
		return false
	}
	first := parts[0]
	if len(first) == 0 || len(first) > 3 || !allDigits(first) || first[0] == '0' {
		return false
	}
	for _, p := range parts[1:] {
		if len(p) != 3 || !allDigits(p) {
			return false
		}
	}
	return true
}

// normalizeAmount reduces a raw amount cell to a bare numeric token plus a sign.
// It drops grouping spaces (incl. non-breaking) and a leading or trailing
// currency token ("SEK", "kr", "€"), then reads the sign, then rejects
// everything else: what is left must be digits and separators only, starting and
// ending with a digit. So "1O5" (letter O) and a lone "," are rejected instead of
// silently becoming 15 and 0.
//
// The currency trim deliberately keeps the sign characters, so "SEK -1234,00"
// stays negative. Trimming them together lost the minus and imported a debit as
// a credit.
func normalizeAmount(s string) (string, bool, bool) {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsSpace(r) { // covers NBSP and narrow NBSP
			b.WriteRune(r)
		}
	}
	s = strings.TrimFunc(b.String(), func(r rune) bool {
		return !isAmountRune(r) && !isSignRune(r)
	})
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	switch {
	case strings.HasPrefix(s, "-"):
		neg, s = true, s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	if strings.HasSuffix(s, "-") { // "1234,56-", still emitted by some exports
		neg, s = true, s[:len(s)-1]
	}
	if s == "" {
		return "", false, false
	}
	for _, r := range s {
		if !isAmountRune(r) {
			return "", false, false
		}
	}
	if !isDigitByte(s[0]) || !isDigitByte(s[len(s)-1]) {
		return "", false, false
	}
	return s, neg, true
}

// oreFromParts turns a digits-only whole and fraction into signed öre, rejecting
// anything that would overflow int64. The fraction is truncated to two digits.
func oreFromParts(whole, frac string, neg bool) (int64, bool) {
	if whole == "" || !allDigits(whole) || !allDigits(frac) {
		return 0, false
	}
	var ore int64
	for i := 0; i < len(whole); i++ {
		// Reject an absurd amount before the accumulation itself wraps int64 to a
		// wrong, possibly sign-flipped figure that would be stored as a real txn.
		// The öre boundary below is the tighter of the two guards.
		if ore > (math.MaxInt64-9)/10 {
			return 0, false
		}
		ore = ore*10 + int64(whole[i]-'0')
	}
	// Guard the (ore*100 + fraction) step at its true boundary. The old guard was
	// `ore > math.MaxInt64/100`, which let ore == MaxInt64/100 through and then
	// wrapped NEGATIVE once the two fraction digits were added: "92233720368547758.99"
	// returned -9223372036854775717 with ok=true.
	if ore > (math.MaxInt64-99)/100 {
		return 0, false
	}
	f := (frac + "00")[:2]
	ore = ore*100 + int64(f[0]-'0')*10 + int64(f[1]-'0')
	if neg {
		ore = -ore
	}
	return ore, true
}

func isAmountRune(r rune) bool { return (r >= '0' && r <= '9') || r == '.' || r == ',' }

// isSignRune reports the characters that carry a sign, so the currency trim
// leaves them for the sign rules. A stray one that the sign rules do not consume
// (e.g. "500)") fails the digits-only check and the cell is rejected.
func isSignRune(r rune) bool { return r == '-' || r == '+' || r == '(' || r == ')' }

func isDigitByte(c byte) bool { return c >= '0' && c <= '9' }

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigitByte(s[i]) {
			return false
		}
	}
	return true
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
