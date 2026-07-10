// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

package bank

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// camt element tags are matched by local name (no namespace in the tag), so this
// works across camt.053.001.02/.04/.08 which differ only by namespace URI.
type camtDoc struct {
	Entries []camtEntry `xml:"BkToCstmrStmt>Stmt>Ntry"`
}

type camtEntry struct {
	Amt          camtAmt  `xml:"Amt"`
	CdtDbtInd    string   `xml:"CdtDbtInd"`
	BookgDt      camtDate `xml:"BookgDt"`
	ValDt        camtDate `xml:"ValDt"`
	AddtlNtryInf string   `xml:"AddtlNtryInf"`
	Ustrd        []string `xml:"NtryDtls>TxDtls>RmtInf>Ustrd"`
	CdtrRef      string   `xml:"NtryDtls>TxDtls>RmtInf>Strd>CdtrRefInf>Ref"`
	DbtrNm       string   `xml:"NtryDtls>TxDtls>RltdPties>Dbtr>Nm"`
	CdtrNm       string   `xml:"NtryDtls>TxDtls>RltdPties>Cdtr>Nm"`
}

type camtAmt struct {
	Value string `xml:",chardata"`
	Ccy   string `xml:"Ccy,attr"`
}

type camtDate struct {
	Dt   string `xml:"Dt"`
	DtTm string `xml:"DtTm"`
}

// ParseCAMT parses an ISO 20022 camt.053 bank statement into normalized entries.
func ParseCAMT(r io.Reader) ([]Entry, error) {
	var doc camtDoc
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("bank: camt decode: %w", err)
	}
	out := make([]Entry, 0, len(doc.Entries))
	for _, e := range doc.Entries {
		ore, ok := parseAmountOre(e.Amt.Value)
		if !ok {
			continue
		}
		if strings.EqualFold(e.CdtDbtInd, "DBIT") {
			ore = -ore
		}
		date, _ := parseDate(firstNonEmpty(e.BookgDt.Dt, e.BookgDt.DtTm, e.ValDt.Dt, e.ValDt.DtTm))
		text := strings.TrimSpace(strings.Join(e.Ustrd, " "))
		if text == "" {
			text = firstNonEmpty(e.DbtrNm, e.CdtrNm, e.AddtlNtryInf)
		}
		out = append(out, Entry{Date: date, AmountOre: ore, Text: text, Ref: strings.TrimSpace(e.CdtrRef)})
	}
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
