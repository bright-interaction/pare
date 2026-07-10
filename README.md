# Pare

Lean, sovereign bookkeeping and invoicing for Swedish companies. Double-entry on
the BAS chart of accounts, moms (incl. förvärvsmoms), SIE 4E import/export, and
an MCP so an AI assistant can run the books without ever seeing who the
counterparties are.

Built by Bright Interaction, first for our own books, open-core. Live at
`pare.brightinteraction.com`.

## Why

Fortnox, Visma and Bokio get expensive fast once you use them properly and want
to automate, and their APIs fight you. Pare is the honest minimum that replaces
them for a service company's own books and hands the accountant a valid SIE file
at year-end.

## Privacy model (two layers)

1. **At rest**: counterparty identities and PII (name, org-nr, personnummer,
   IBAN, address, email) are encrypted with envelope AES-256-GCM. A per-company
   data key (DEK) is wrapped by the master key (`PARE_MASTER_KEY`); a `key_id`
   fingerprint on each company makes future rotation possible. Amounts, account
   codes and dates stay in clear so the ledger is queryable. A database dump
   leaks no identities.
2. **At the MCP/LLM boundary**: Shield tokenizes identities before anything
   reaches the model (`Kund#A3F2`, never the real name). Amounts stay visible so
   the assistant can categorize, reconcile and prepare the momsrapport. A CI
   completeness guard fails the build if a PII field is left untagged.

## Features

- Double-entry ledger on the BAS 2025/26 chart; verifikat are DB-immutable,
  corrections are reversing entries (rättelseverifikat).
- Invoicing: draft -> finalize (auto-posts a balanced verifikat) -> PDF; gap-free
  per-year numbering; multi-currency; kreditfaktura (credit notes) + refunds.
- Payments: settle an invoice with automatic account assignment (1510/2440),
  currency difference (3960/7960), öresavrundning (3740); partial payments; a
  smart match from an incoming amount to the right open invoice.
- Leverantörsfaktura (AP) with förvärvsmoms self-assessment on foreign services.
- Moms: full momsdeklaration boxes (rutor), period-scoped.
- SIE 4 import (with #IB opening balances) and export (with #IB/#UB/#RES).
- Bank reconciliation: import a camt.053 or CSV statement, auto-match credits to
  open invoices, one-click book (or categorize to any account). The payer text is
  encrypted at rest and never crosses the AI boundary.
- Receipts/underlag: encrypted document capture attached to supplier invoices.
- Reports: resultat- + balansräkning (period-correct), momsdeklaration, huvudbok,
  verifikationslista, reskontra (aged receivables + payables), CSV.
- Year-end close (bokslut) with first-class fiscal years + period lock.
- Trust: period lock, audit log with an HMAC hash chain (tamper-evident), undo,
  read-only accountant (revisor) role.
- Email: send invoices + payment reminders over SMTP (optional).
- MCP: composite read tools + write tools with a per-write ceiling and dry_run.

## Configuration (`PARE_*`)

| Var | Required | Purpose |
|-----|----------|---------|
| `PARE_DATABASE_URL` | yes | Postgres DSN |
| `PARE_MASTER_KEY` | yes | 32 bytes base64; wraps per-company DEKs |
| `PARE_SHIELD_KEY` | for MCP | 32 bytes base64; MCP tokenization vault |
| `PARE_MCP_KEY` | for MCP | org key gating `/mcp` (min 16 chars) |
| `PARE_MCP_MAX_ORE` | no | per-AI-write ceiling in öre (default 50 000 000) |
| `PARE_GOTENBERG_URL` | no | PDF renderer (default `http://gotenberg:3000`) |
| `PARE_SMTP_HOST/PORT/USER/PASS/FROM/FROM_NAME/TLS` | no | email (no-op if unset) |
| `FLARE_DSN` | no | error reporting to a Sentry-compatible sink |
| `PARE_INSECURE_COOKIES` | no | `1` disables Secure cookies for local HTTP dev |

See `.env.example` for the full list with comments, including the next-step
integration placeholders (PSD2 bank feed, Mollie payment gateway). The live
config status (email/MCP/Flare on or off) is shown on the in-app `/api` page.

## API (MCP)

JSON-RPC 2.0 over `POST /mcp`, org-key auth in the `Authorization` header. Read
tools: `pare_financial_overview`, `pare_unpaid_invoices`, `pare_trial_balance`
(period), `pare_moms_report` (period), `pare_export_sie`, `pare_recent_activity`,
`pare_match_payment`. Write tools (ceiling + `dry_run`): `pare_post_verification`,
`pare_record_payment`. Every counterparty identity in a response is Shield-
tokenized. The live, always-accurate tool list is on the in-app `/api` page.

## Integrations

SIE 4 (Fortnox/Visma/accountant), Gotenberg (PDF), SMTP (Resend/SES), Flare
(errors), and the MCP (Claude).

## Layout

- `internal/crypto` envelope encryption (at rest)
- `internal/ledger` double-entry engine, BAS classification, reports/statements
- `internal/moms` VAT engine (output, input, förvärvsmoms, momsdeklaration)
- `internal/sie` SIE 4 read/write (CP437)
- `internal/invoice` invoice model -> verifikat lines
- `internal/store` persistence (sqlc), all business logic
- `internal/shield` MCP-boundary tokenization
- `internal/mcp` the `pare` MCP server
- `internal/email` SMTP mailer
- `internal/handler` chi router + server-rendered operator UI
- `internal/render` gotenberg PDF client + invoice template
- `internal/config`, `internal/db` (goose migrations + sqlc), `cmd/server`

## Build, test, run

```
go build ./...
# tests need a Postgres:  PARE_TEST_DATABASE_URL=postgres://... go test ./...
```

To self-host, copy `.env.example` to `.env`, fill `PARE_MASTER_KEY` (and the MCP
keys if you want the AI assistant), then `docker compose -f deploy/docker-compose.yml up -d`
and open http://localhost:8080. Migrations run on boot (goose); `SyncChart`
backfills chart additions.

## License

Core: AGPL-3.0-or-later. A private `pro` build overlay (Go build tag `pro`) is
reserved for multi-company, bank feeds, Peppol, payroll and the hosted SaaS.
