// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/bright-interaction/pare/internal/ledger"
	"github.com/bright-interaction/pare/internal/moms"
)

// register wires all tools. Read tools are composite (pre-joined) so the AI
// gets a useful answer in one call; write tools mutate the books.
func (s *Server) register() {
	s.add(tool{
		name:   "pare_financial_overview",
		desc:   "Snapshot of the books: profit/loss, VAT position, and unpaid invoices, all for the active company. No identities.",
		schema: emptySchema(),
		proto:  &overviewResult{},
		run:    runOverview,
	})
	s.add(tool{
		name:   "pare_unpaid_invoices",
		desc:   "Finalized invoices that are not yet paid, with amounts and due dates. Customer identities are tokenized.",
		schema: emptySchema(),
		proto:  &unpaidResult{},
		run:    runUnpaid,
	})
	s.add(tool{
		name:   "pare_trial_balance",
		desc:   "Per-account net balances (huvudbok summary). Optional from/to (YYYY-MM-DD) scope it to a period; omit for all time. Account codes and amounts only.",
		schema: periodSchema(),
		proto:  &trialBalanceResult{},
		run:    runTrialBalance,
	})
	s.add(tool{
		name:   "pare_moms_report",
		desc:   "The momsdeklaration boxes (rutor). Optional from/to (YYYY-MM-DD) scope it to a filing period (recommended); omit for all time.",
		schema: periodSchema(),
		proto:  &momsResult{},
		run:    runMoms,
	})
	s.add(tool{
		name:   "pare_export_sie",
		desc:   "Export the full ledger as a SIE type 4 file (base64) for an accountant or Fortnox/Visma to import.",
		schema: emptySchema(),
		proto:  &sieResult{},
		run:    runExportSIE,
	})
	s.add(tool{
		name:   "pare_recent_activity",
		desc:   "The audit log: recent postings by ai, users and the system, with actor attribution. No identities. Undo is a human-only action in the UI.",
		schema: emptySchema(),
		proto:  &auditResult{},
		run:    runAudit,
	})
	s.add(tool{
		name:  "pare_post_verification",
		desc:  "Post a manual balanced verifikat (debit must equal credit). Immutable once posted.",
		write: true,
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"series":      map[string]any{"type": "string", "description": "voucher series, e.g. A"},
				"date":        map[string]any{"type": "string", "description": "YYYY-MM-DD"},
				"description": map[string]any{"type": "string"},
				"lines": map[string]any{"type": "array", "items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"account":    map[string]any{"type": "string"},
						"debit_ore":  map[string]any{"type": "integer"},
						"credit_ore": map[string]any{"type": "integer"},
						"vat_code":   map[string]any{"type": "string"},
					},
					"required": []string{"account"},
				}},
				"dry_run": map[string]any{"type": "boolean", "description": "if true, validate and preview only; nothing is committed"},
			},
			"required": []string{"series", "date", "lines"},
		},
		proto: &postResult{},
		run:   runPostVerification,
	})
	s.add(tool{
		name: "pare_match_payment",
		desc: "Given an incoming payment amount (received_sek_ore), return the open customer invoices whose outstanding balance matches it, best first. Use to reconcile a bank payment to the right invoice before calling pare_record_payment. Customer identities are tokenized.",
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"received_sek_ore": map[string]any{"type": "integer", "description": "the amount received, in öre (SEK)"},
			},
			"required": []string{"received_sek_ore"},
		},
		proto: &matchResult{},
		run:   runMatchPayment,
	})
	s.add(tool{
		name:  "pare_record_payment",
		desc:  "Settle a finalized invoice: book the received amount to a bank account, clear Kundfordringar (1510), and post any currency difference (3960/7960). Reference the invoice by its number.",
		write: true,
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"invoice_number":   map[string]any{"type": "string", "description": "the finalized invoice's number, e.g. 2026-0001"},
				"date":             map[string]any{"type": "string", "description": "payment date, YYYY-MM-DD"},
				"received_sek_ore": map[string]any{"type": "integer", "description": "amount actually received, in öre (SEK). For a foreign-currency invoice this is the SEK that landed in the bank."},
				"account":          map[string]any{"type": "string", "description": "bank account to debit; defaults to 1930"},
				"dry_run":          map[string]any{"type": "boolean", "description": "if true, validate and preview only; nothing is committed"},
			},
			"required": []string{"invoice_number", "date", "received_sek_ore"},
		},
		proto: &paymentResult{},
		run:   runRecordPayment,
	})
}

func emptySchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

// checkCeiling rejects an AI write whose amount (öre) exceeds the configured
// per-write ceiling. A ceiling of 0 disables the cap.
func checkCeiling(tc toolCtx, ore int64) error {
	if tc.maxWriteOre > 0 && ore > tc.maxWriteOre {
		return errCeiling
	}
	return nil
}

func periodSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"from": map[string]any{"type": "string", "description": "period start, YYYY-MM-DD (optional)"},
			"to":   map[string]any{"type": "string", "description": "period end, YYYY-MM-DD (optional)"},
		},
	}
}

// periodBalances returns account balances scoped to an optional [from,to] period
// (all time when both are empty). Bad dates fall back to all time.
func periodBalances(ctx context.Context, tc toolCtx, args json.RawMessage) (map[string]ledger.Amount, []ledger.AccountBalance, error) {
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	_ = json.Unmarshal(args, &in)
	from, ferr := time.Parse("2006-01-02", in.From)
	to, terr := time.Parse("2006-01-02", in.To)
	var tb []ledger.AccountBalance
	var err error
	if ferr == nil && terr == nil {
		tb, err = tc.store.TrialBalanceBetween(ctx, tc.company, from, to)
	} else {
		tb, err = tc.store.TrialBalance(ctx, tc.company)
	}
	if err != nil {
		return nil, nil, err
	}
	bal := make(map[string]ledger.Amount, len(tb))
	for _, r := range tb {
		bal[r.Account] = r.Net
	}
	return bal, tb, nil
}

// --- result types (json names are checked by shield_completeness_test) ---

type overviewResult struct {
	ResultKr       string `json:"result_kr"`
	OutputVatKr    string `json:"output_vat_kr"`
	InputVatKr     string `json:"input_vat_kr"`
	MomsToPayKr    string `json:"moms_to_pay_kr"`
	UnpaidInvoices int    `json:"unpaid_invoices"`
	UnpaidTotalKr  string `json:"unpaid_total_kr"`
}

type unpaidResult struct {
	Invoices []unpaidRow `json:"invoices"`
}

type unpaidRow struct {
	Number   string `json:"number"`
	Customer string `json:"customer" shield:"tokenize,kind=name"`
	Orgnr    string `json:"customer_orgnr" shield:"tokenize,kind=orgnr"`
	Total    string `json:"total"`
	Currency string `json:"currency"`
	TotalSEK string `json:"total_sek"`
	DueDate  string `json:"due_date"`
}

type matchResult struct {
	Matches []matchRow `json:"matches"`
}

type matchRow struct {
	Number     string `json:"number"`
	Customer   string `json:"customer" shield:"tokenize,kind=name"`
	OutstandKr string `json:"outstanding_kr"`
	DueDate    string `json:"due_date"`
	ExactMatch bool   `json:"exact_match"`
}

type trialBalanceResult struct {
	Rows       []tbRow `json:"rows"`
	TotalNetKr string  `json:"total_net_kr"`
}

type tbRow struct {
	Account string `json:"account"`
	Class   int    `json:"class"`
	NetKr   string `json:"net_kr"`
}

type momsResult struct {
	Box05Kr string `json:"box_05_net_sales_kr"`
	Box10Kr string `json:"box_10_output_25_kr"`
	Box11Kr string `json:"box_11_output_12_kr"`
	Box12Kr string `json:"box_12_output_6_kr"`
	Box21Kr string `json:"box_21_eu_services_bought_kr"`
	Box22Kr string `json:"box_22_non_eu_services_bought_kr"`
	Box30Kr string `json:"box_30_reverse_charge_output_kr"`
	Box39Kr string `json:"box_39_eu_services_sold_kr"`
	Box48Kr string `json:"box_48_input_kr"`
	Box49Kr string `json:"box_49_to_pay_kr"`
}

type sieResult struct {
	Filename     string `json:"filename"`
	VoucherCount int    `json:"voucher_count"`
	Note         string `json:"note"`
}

type postResult struct {
	VerificationID string `json:"verification_id"`
	Ok             bool   `json:"ok"`
	DryRun         bool   `json:"dry_run"`
	Note           string `json:"note"`
}

type paymentResult struct {
	VerificationID string `json:"verification_id"`
	InvoiceNumber  string `json:"invoice_number"`
	Ok             bool   `json:"ok"`
	DryRun         bool   `json:"dry_run"`
	Note           string `json:"note"`
}

type auditResult struct {
	Entries []auditRow `json:"entries"`
}

type auditRow struct {
	At     string `json:"at"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Target string `json:"target"`
	// Detail is intentionally omitted: audit_log.detail can carry operator
	// free-text (e.g. a period-unlock reason) that may contain identities, which
	// must not cross the LLM boundary. The full detail stays in the UI logg.
}

// --- handlers ---

func runOverview(ctx context.Context, tc toolCtx, _ json.RawMessage) (any, error) {
	tb, err := tc.store.TrialBalance(ctx, tc.company)
	if err != nil {
		return nil, err
	}
	bal := make(map[string]ledger.Amount, len(tb))
	var result ledger.Amount
	for _, r := range tb {
		bal[r.Account] = r.Net
		if r.Class.IsResult() {
			result += r.Net
		}
	}
	d := moms.Report(bal)
	unpaid, err := tc.store.UnpaidInvoices(ctx, tc.company)
	if err != nil {
		return nil, err
	}
	var unpaidTotal ledger.Amount
	for _, u := range unpaid {
		unpaidTotal += u.TotalSEK
	}
	return &overviewResult{
		ResultKr:       (-result).String(),
		OutputVatKr:    (d.Box10 + d.Box11 + d.Box12).String(),
		InputVatKr:     d.Box48.String(),
		MomsToPayKr:    d.Box49.String(),
		UnpaidInvoices: len(unpaid),
		UnpaidTotalKr:  unpaidTotal.String(),
	}, nil
}

func runUnpaid(ctx context.Context, tc toolCtx, _ json.RawMessage) (any, error) {
	unpaid, err := tc.store.UnpaidInvoices(ctx, tc.company)
	if err != nil {
		return nil, err
	}
	res := &unpaidResult{}
	for _, u := range unpaid {
		res.Invoices = append(res.Invoices, unpaidRow{
			Number:   u.Number,
			Customer: u.CustomerName,
			Orgnr:    u.CustomerOrgNr,
			Total:    u.Total.String(),
			Currency: u.Currency,
			TotalSEK: u.TotalSEK.String(),
			DueDate:  u.DueDate,
		})
	}
	return res, nil
}

func runMatchPayment(ctx context.Context, tc toolCtx, args json.RawMessage) (any, error) {
	var in struct {
		ReceivedSEKOre int64 `json:"received_sek_ore"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errInvalidArgs
	}
	matches, err := tc.store.MatchOpenInvoices(ctx, tc.company, ledger.Amount(in.ReceivedSEKOre))
	if err != nil {
		return nil, err
	}
	res := &matchResult{}
	for _, m := range matches {
		out := m.TotalSEK - m.AmountPaid
		res.Matches = append(res.Matches, matchRow{
			Number: m.Number, Customer: m.CustomerName, OutstandKr: out.String(),
			DueDate: m.DueDate, ExactMatch: out == ledger.Amount(in.ReceivedSEKOre),
		})
	}
	return res, nil
}

func runTrialBalance(ctx context.Context, tc toolCtx, args json.RawMessage) (any, error) {
	_, tb, err := periodBalances(ctx, tc, args)
	if err != nil {
		return nil, err
	}
	res := &trialBalanceResult{}
	var total ledger.Amount
	for _, r := range tb {
		res.Rows = append(res.Rows, tbRow{Account: r.Account, Class: int(r.Class), NetKr: r.Net.String()})
		total += r.Net
	}
	res.TotalNetKr = total.String()
	return res, nil
}

func runMoms(ctx context.Context, tc toolCtx, args json.RawMessage) (any, error) {
	bal, _, err := periodBalances(ctx, tc, args)
	if err != nil {
		return nil, err
	}
	d := moms.Report(bal)
	return &momsResult{
		Box05Kr: d.Box05.String(), Box10Kr: d.Box10.String(), Box11Kr: d.Box11.String(),
		Box12Kr: d.Box12.String(), Box21Kr: d.Box21.String(), Box22Kr: d.Box22.String(),
		Box30Kr: d.Box30.String(), Box39Kr: d.Box39.String(),
		Box48Kr: d.Box48.String(), Box49Kr: d.Box49.String(),
	}, nil
}

func runExportSIE(ctx context.Context, tc toolCtx, _ json.RawMessage) (any, error) {
	// The SIE file embeds the company org-nr and every voucher's free-text
	// description, which can carry counterparty identities. Shield cannot
	// tokenize inside an opaque blob, so the raw file is NOT returned across the
	// LLM boundary; report only its shape and point to the UI download.
	exp, err := tc.store.ExportSIE(ctx, tc.company, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return &sieResult{
		Filename:     "pare-export.se",
		VoucherCount: len(exp.Vouchers),
		Note:         "The SIE file contains identifying free text and is not exposed to the assistant. Download it from the Rapporter page in the Pare UI.",
	}, nil
}

func runAudit(ctx context.Context, tc toolCtx, _ json.RawMessage) (any, error) {
	entries, err := tc.store.ListAudit(ctx, tc.company, 50)
	if err != nil {
		return nil, err
	}
	res := &auditResult{}
	for _, e := range entries {
		res.Entries = append(res.Entries, auditRow{
			At:     e.At.Format(time.RFC3339),
			Actor:  e.Actor,
			Action: e.Action,
			Target: e.TargetType + ":" + e.TargetID,
		})
	}
	return res, nil
}

func runPostVerification(ctx context.Context, tc toolCtx, args json.RawMessage) (any, error) {
	var in struct {
		Series      string `json:"series"`
		Date        string `json:"date"`
		Description string `json:"description"`
		DryRun      bool   `json:"dry_run"`
		Lines       []struct {
			Account   string `json:"account"`
			DebitOre  int64  `json:"debit_ore"`
			CreditOre int64  `json:"credit_ore"`
			VATCode   string `json:"vat_code"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errInvalidArgs
	}
	date, err := time.Parse("2006-01-02", in.Date)
	if err != nil {
		return nil, errBadDate
	}
	lines := make([]ledger.Line, 0, len(in.Lines))
	var debitTotal int64
	for _, l := range in.Lines {
		lines = append(lines, ledger.Line{
			Account: l.Account,
			Debit:   ledger.Amount(l.DebitOre),
			Credit:  ledger.Amount(l.CreditOre),
			VATCode: l.VATCode,
		})
		debitTotal += l.DebitOre
	}
	if err := checkCeiling(tc, debitTotal); err != nil {
		return nil, err
	}
	if in.DryRun {
		// Validate (balance + non-negative) without committing, so the assistant
		// can propose and a human can then post it in the UI.
		if err := (ledger.Verification{Series: in.Series, Date: date, Description: in.Description, Lines: lines}).Validate(); err != nil {
			return nil, err
		}
		return &postResult{DryRun: true, Ok: false, Note: "Balanserad. Inget bokfördes (dry_run). Be en människa bekräfta i Pare."}, nil
	}
	verID, err := tc.store.PostVerification(ctx, tc.company, in.Series, date, in.Description, lines, uuid.Nil)
	if err != nil {
		return nil, err
	}
	return &postResult{VerificationID: verID.String(), Ok: true}, nil
}

func runRecordPayment(ctx context.Context, tc toolCtx, args json.RawMessage) (any, error) {
	var in struct {
		InvoiceNumber  string `json:"invoice_number"`
		Date           string `json:"date"`
		ReceivedSEKOre int64  `json:"received_sek_ore"`
		Account        string `json:"account"`
		DryRun         bool   `json:"dry_run"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errInvalidArgs
	}
	date, err := time.Parse("2006-01-02", in.Date)
	if err != nil {
		return nil, errBadDate
	}
	account := in.Account
	if account == "" {
		account = "1930"
	}
	if err := checkCeiling(tc, in.ReceivedSEKOre); err != nil {
		return nil, err
	}
	if in.DryRun {
		return &paymentResult{DryRun: true, Ok: false, InvoiceNumber: in.InvoiceNumber, Note: "Inget bokfördes (dry_run). Be en människa bekräfta betalningen i Pare."}, nil
	}
	verID, err := tc.store.RecordPaymentByNumber(ctx, tc.company, in.InvoiceNumber, date, account, ledger.Amount(in.ReceivedSEKOre))
	if err != nil {
		return nil, err
	}
	return &paymentResult{VerificationID: verID.String(), InvoiceNumber: in.InvoiceNumber, Ok: true}, nil
}
