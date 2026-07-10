// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bright-interaction/pare/internal/crypto"
	"github.com/bright-interaction/pare/internal/invoice"
	"github.com/bright-interaction/pare/internal/ledger"
	"github.com/bright-interaction/pare/internal/moms"
	"github.com/bright-interaction/pare/internal/store"
	"github.com/bright-interaction/pare/internal/testdb"
)

const testKey = "test-mcp-key"

func day(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func setup(t *testing.T) (http.Handler, func()) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testdb.New(t, "mcp"))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	testdb.Reset(t, pool)
	ctx := context.Background()

	key, _ := crypto.NewDEK()
	kek, _ := crypto.NewKEK(key)
	st := store.New(pool, kek)

	co, err := st.BootstrapCompany(ctx, "Bright Interaction AB", "556000-0000")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	cust, err := st.CreateCounterparty(ctx, co, store.Counterparty{Kind: "customer", Name: "Advokatbyrån Nord AB", OrgNr: "556677-8899"})
	if err != nil {
		t.Fatalf("counterparty: %v", err)
	}
	invID, err := st.CreateInvoice(ctx, co, cust, invoice.Invoice{Lines: []invoice.Line{
		{Description: "Arvode", QuantityMilli: 1000, UnitPriceOre: ledger.SEK(10000, 0), VATCode: moms.SE25},
	}})
	if err != nil {
		t.Fatalf("invoice: %v", err)
	}
	if _, _, err := st.FinalizeInvoice(ctx, co, invID, day("2026-02-01"), day("2026-03-03")); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	shieldKey, _ := crypto.NewDEK()
	srv, err := New(st, pool, shieldKey, testKey, 0)
	if err != nil {
		t.Fatalf("mcp: %v", err)
	}
	return srv.Handler(), func() { pool.Close() }
}

func call(t *testing.T, h http.Handler, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	if key != "" {
		req.Header.Set("X-Api-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthRequired(t *testing.T) {
	h, done := setup(t)
	defer done()
	rec := call(t, h, "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-key request got %d, want 401", rec.Code)
	}
	rec = call(t, h, "wrong", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-key request got %d, want 401", rec.Code)
	}
}

func TestToolsList(t *testing.T) {
	h, done := setup(t)
	defer done()
	rec := call(t, h, testKey, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("pare_unpaid_invoices")) ||
		!bytes.Contains(rec.Body.Bytes(), []byte("pare_export_sie")) {
		t.Fatalf("tools/list missing tools: %s", rec.Body.String())
	}
}

func TestUnpaidTokenized(t *testing.T) {
	h, done := setup(t)
	defer done()
	rec := call(t, h, testKey, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"pare_unpaid_invoices","arguments":{}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	text := toolText(t, rec.Body.Bytes())

	// identity tokenized, plaintext absent
	if !bytes.Contains([]byte(text), []byte("[shield:name:tok_")) {
		t.Errorf("customer name not tokenized: %s", text)
	}
	if bytes.Contains([]byte(text), []byte("Nord")) || bytes.Contains([]byte(text), []byte("556677")) {
		t.Errorf("plaintext identity leaked to LLM boundary: %s", text)
	}
	// amount stays visible so the AI can reconcile
	if !bytes.Contains([]byte(text), []byte("12500,00")) {
		t.Errorf("amount not visible: %s", text)
	}
}

func TestOverviewNoIdentities(t *testing.T) {
	h, done := setup(t)
	defer done()
	rec := call(t, h, testKey, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"pare_financial_overview","arguments":{}}}`)
	text := toolText(t, rec.Body.Bytes())
	if bytes.Contains([]byte(text), []byte("Nord")) {
		t.Errorf("overview leaked an identity: %s", text)
	}
	if !bytes.Contains([]byte(text), []byte("moms_to_pay_kr")) {
		t.Errorf("overview missing moms figure: %s", text)
	}
}

func TestExportSIENotRawOverMCP(t *testing.T) {
	h, done := setup(t)
	defer done()
	rec := call(t, h, testKey, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"pare_export_sie","arguments":{}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	body := rec.Body.Bytes()
	// The raw SIE (which embeds org-nr + free-text descriptions) must NOT cross
	// the LLM boundary; only shape + a UI-download pointer.
	if bytes.Contains(body, []byte("sie_base64")) {
		t.Errorf("raw SIE base64 still exposed over MCP: %s", body)
	}
	if !bytes.Contains(body, []byte("voucher_count")) || !bytes.Contains(body, []byte("Rapporter")) {
		t.Errorf("export tool should return only metadata + a UI-download note: %s", body)
	}
}

// toolText extracts the text content from a tools/call JSON-RPC response.
func toolText(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if resp.Result.IsError {
		t.Fatalf("tool returned error: %s", resp.Result.Content[0].Text)
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("no content: %s", body)
	}
	return resp.Result.Content[0].Text
}
