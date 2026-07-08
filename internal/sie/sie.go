// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package sie writes and reads SIE type 4 files (the .SE format Fortnox, Visma
// and accountants import). Verified against the SIE 4B spec on sie.se
// 2026-07-09. Key rules: CP437 (PC8) encoding, LF line endings, '.' decimal
// separator, debit-positive / credit-negative signed amounts, YYYYMMDD dates,
// and each voucher's transaction amounts must sum to zero.
package sie

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

// Program identifies the exporter in the #PROGRAM record.
const (
	programName    = "Pare"
	programVersion = "0.1"
)

// Account is one #KONTO row.
type Account struct {
	Number string
	Name   string
}

// Line is one #TRANS row. Amount is signed öre: debit positive, credit negative.
type Line struct {
	Account string
	Amount  int64
	Text    string
}

// Voucher is one #VER block.
type Voucher struct {
	Series string
	Number int
	Date   time.Time
	Text   string
	Lines  []Line
}

// Export is everything needed to write (or the result of reading) a SIE 4 file.
type Export struct {
	CompanyName string
	OrgNr       string
	YearStart   time.Time
	YearEnd     time.Time
	Generated   time.Time
	Accounts    []Account
	Vouchers    []Voucher
}

// Balances checks that every voucher's transaction amounts sum to zero.
func (e Export) Balances() error {
	for _, v := range e.Vouchers {
		var sum int64
		for _, l := range v.Lines {
			sum += l.Amount
		}
		if sum != 0 {
			return fmt.Errorf("sie: voucher %s%d does not balance (sum %d)", v.Series, v.Number, sum)
		}
	}
	return nil
}

// Write emits a SIE type 4 file, CP437-encoded, to w.
func (e Export) Write(w io.Writer) error {
	if err := e.Balances(); err != nil {
		return err
	}
	var b strings.Builder
	line := func(format string, a ...any) {
		fmt.Fprintf(&b, format, a...)
		b.WriteByte('\n')
	}

	line("#FLAGGA 0")
	line("#PROGRAM %s %s", quote(programName), quote(programVersion))
	line("#FORMAT PC8")
	line("#GEN %s", ymd(e.Generated))
	line("#SIETYP 4")
	line("#ORGNR %s", e.OrgNr)
	line("#FNAMN %s", quote(e.CompanyName))
	line("#RAR 0 %s %s", ymd(e.YearStart), ymd(e.YearEnd))
	line("#KPTYP EUBAS97")
	line("#VALUTA SEK")
	for _, a := range e.Accounts {
		line("#KONTO %s %s", a.Number, quote(a.Name))
	}
	for _, v := range e.Vouchers {
		line("#VER %s %d %s %s", verSeries(v.Series), v.Number, ymd(v.Date), quote(v.Text))
		line("{")
		for _, l := range v.Lines {
			if l.Text == "" {
				line("#TRANS %s {} %s", l.Account, amount(l.Amount))
			} else {
				line("#TRANS %s {} %s %s %s", l.Account, amount(l.Amount), ymd(v.Date), quote(l.Text))
			}
		}
		line("}")
	}

	encoded, _, err := transform.String(charmap.CodePage437.NewEncoder(), b.String())
	if err != nil {
		return fmt.Errorf("sie: cp437 encode: %w", err)
	}
	_, err = io.WriteString(w, encoded)
	return err
}

// Parse reads a SIE type 4 file (CP437) and returns its company info, chart and
// vouchers. Robust to the object-list field and to LF or CRLF line endings.
func Parse(r io.Reader) (Export, error) {
	var e Export
	dec := transform.NewReader(r, charmap.CodePage437.NewDecoder())
	sc := bufio.NewScanner(dec)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var cur *Voucher
	for sc.Scan() {
		toks := tokenize(strings.TrimRight(sc.Text(), "\r"))
		if len(toks) == 0 {
			continue
		}
		switch toks[0] {
		case "#FNAMN":
			if len(toks) > 1 {
				e.CompanyName = toks[1]
			}
		case "#ORGNR":
			if len(toks) > 1 {
				e.OrgNr = toks[1]
			}
		case "#RAR":
			if len(toks) > 3 && toks[1] == "0" {
				e.YearStart = parseYMD(toks[2])
				e.YearEnd = parseYMD(toks[3])
			}
		case "#KONTO":
			if len(toks) > 2 {
				e.Accounts = append(e.Accounts, Account{Number: toks[1], Name: toks[2]})
			}
		case "#VER":
			v := Voucher{}
			if len(toks) > 1 {
				v.Series = toks[1]
			}
			if len(toks) > 2 {
				v.Number, _ = strconv.Atoi(toks[2])
			}
			if len(toks) > 3 {
				v.Date = parseYMD(toks[3])
			}
			if len(toks) > 4 {
				v.Text = toks[4]
			}
			cur = &v
		case "{":
			// voucher body opens; lines follow
		case "}":
			if cur != nil {
				e.Vouchers = append(e.Vouchers, *cur)
				cur = nil
			}
		case "#TRANS":
			// #TRANS account {objectlist} amount [transdate] [text] ...
			if cur != nil && len(toks) >= 4 {
				l := Line{Account: toks[1], Amount: parseAmount(toks[3])}
				if len(toks) >= 6 {
					l.Text = toks[5]
				}
				cur.Lines = append(cur.Lines, l)
			}
		}
	}
	return e, sc.Err()
}

// tokenize splits a SIE line into fields, keeping "quoted strings" (with \"
// escapes) and {object lists} as single tokens; quotes are stripped.
func tokenize(line string) []string {
	var toks []string
	i, n := 0, len(line)
	for i < n {
		switch c := line[i]; {
		case c == ' ' || c == '\t':
			i++
		case c == '"':
			i++
			var sb strings.Builder
			for i < n {
				if line[i] == '\\' && i+1 < n && line[i+1] == '"' {
					sb.WriteByte('"')
					i += 2
					continue
				}
				if line[i] == '"' {
					i++
					break
				}
				sb.WriteByte(line[i])
				i++
			}
			toks = append(toks, sb.String())
		case c == '{':
			depth, start := 0, i
			for i < n {
				if line[i] == '{' {
					depth++
				}
				if line[i] == '}' {
					depth--
					if depth == 0 {
						i++
						break
					}
				}
				i++
			}
			toks = append(toks, line[start:i])
		default:
			start := i
			for i < n && line[i] != ' ' && line[i] != '\t' {
				i++
			}
			toks = append(toks, line[start:i])
		}
	}
	return toks
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func verSeries(s string) string {
	if s == "" {
		return `""`
	}
	return s
}

func ymd(t time.Time) string {
	return t.Format("20060102")
}

func parseYMD(s string) time.Time {
	t, _ := time.Parse("20060102", s)
	return t
}

// amount formats signed öre as a SIE decimal (e.g. -13200 öre -> "-132.00").
func amount(ore int64) string {
	sign := ""
	if ore < 0 {
		sign = "-"
		ore = -ore
	}
	return fmt.Sprintf("%s%d.%02d", sign, ore/100, ore%100)
}

// parseAmount reads a SIE decimal string into signed öre.
func parseAmount(s string) int64 {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	whole, frac, _ := strings.Cut(s, ".")
	kr, _ := strconv.ParseInt(whole, 10, 64)
	var ore int64
	if frac != "" {
		ore, _ = strconv.ParseInt((frac + "00")[:2], 10, 64)
	}
	v := kr*100 + ore
	if neg {
		v = -v
	}
	return v
}
