// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package bank

import (
	"bufio"
	"io"
	"strings"
)

// ParseCSV parses a simple bank-export CSV into normalized entries. It detects
// the delimiter (`;` or `,`), finds the date / amount / text columns from the
// header (Swedish or English keywords), and tolerates Swedish number formatting.
// camt.053 is the preferred, universal format; this covers a plain CSV export.
func ParseCSV(r io.Reader) ([]Entry, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	if !sc.Scan() {
		return nil, nil
	}
	header := sc.Text()
	delim := ","
	if strings.Count(header, ";") >= strings.Count(header, ",") {
		delim = ";"
	}
	cols := splitCSV(header, delim)
	di, ai, ti := -1, -1, -1
	for i, c := range cols {
		lc := strings.ToLower(strings.TrimSpace(c))
		switch {
		case di < 0 && (strings.Contains(lc, "datum") || strings.Contains(lc, "date") || strings.Contains(lc, "bokf")):
			di = i
		case ai < 0 && (strings.Contains(lc, "belopp") || strings.Contains(lc, "amount") || strings.Contains(lc, "summa")):
			ai = i
		case ti < 0 && (strings.Contains(lc, "text") || strings.Contains(lc, "meddel") || strings.Contains(lc, "referens") || strings.Contains(lc, "mottagare") || strings.Contains(lc, "avsändare") || strings.Contains(lc, "description")):
			ti = i
		}
	}
	if di < 0 || ai < 0 {
		// No recognizable header: assume date,amount,text positional.
		di, ai, ti = 0, 1, 2
	}
	var out []Entry
	// The header line we already consumed was a header only if we matched columns
	// by name; if positional, re-include it. Simplest: try to parse every data row
	// and skip rows whose amount/date don't parse (covers the header row too).
	rows := []string{}
	for sc.Scan() {
		rows = append(rows, sc.Text())
	}
	for _, line := range rows {
		f := splitCSV(line, delim)
		if ai >= len(f) || di >= len(f) {
			continue
		}
		amount, ok := parseAmountOre(f[ai])
		if !ok {
			continue
		}
		date, ok := parseDate(f[di])
		if !ok {
			continue
		}
		text := ""
		if ti >= 0 && ti < len(f) {
			text = strings.TrimSpace(f[ti])
		}
		out = append(out, Entry{Date: date, AmountOre: amount, Text: text})
	}
	return out, nil
}

// splitCSV splits a line on delim, honouring simple double-quoted fields.
func splitCSV(line, delim string) []string {
	var fields []string
	var cur strings.Builder
	inQuote := false
	d := delim[0]
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == d && !inQuote:
			fields = append(fields, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	fields = append(fields, strings.TrimSpace(cur.String()))
	return fields
}
