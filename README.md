# Pare

Lean, sovereign bookkeeping and invoicing for Swedish companies. Double-entry on
the BAS chart of accounts, moms handling, SIE 4E export, and an MCP so an AI
assistant can run the books without ever seeing who the counterparties are.

Built by Bright Interaction, first for our own books, open-core.

## Why

Fortnox, Visma and Bokio get expensive fast once you use them properly and want
to automate, and their APIs fight you. Pare is the honest minimum that replaces
them for a service company's own books and hands the accountant a valid SIE file
at year-end.

## Privacy model (two layers)

1. At rest: counterparty identities and PII (name, org-nr, personnummer, IBAN,
   address, free-text notes, receipt blobs) are encrypted with envelope
   AES-256-GCM. A per-company data key (DEK) is wrapped by the master key
   (`PARE_MASTER_KEY`). Amounts, account codes and dates stay in clear so the
   ledger is queryable. A database dump leaks no identities.
2. At the MCP/LLM boundary: Shield tokenizes identities before anything reaches
   the model (`Kund#A3F2`, never the real name or org-nr). Amounts stay visible
   so the assistant can categorize, reconcile and prepare the momsrapport.

## Open core

Core is AGPL-3.0 (single-company ledger, invoicing, moms, SIE, reports, MCP,
both crypto layers). A private `pro` build overlay (Go build tag `pro`) adds
multi-company, bank feeds, Peppol e-invoice, payroll and the hosted SaaS.

## Status

Phase 1 (ledger core + at-rest crypto) in progress. See the build plan for the
remaining phases: invoicing + moms + SIE, the Shield MCP, the operator UI, and
deploy.

## Layout

- `internal/crypto`: envelope encryption (at rest)
- `internal/ledger`: double-entry engine, BAS classification, reports
- `internal/config`: `PARE_*` configuration
- `internal/db`: goose migrations + sqlc queries
- `cmd/server`: HTTP entrypoint
