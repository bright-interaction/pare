# Licensing and open core

Pare is open core (fair-code).

## Core (this repository): Pare Sustainable Use License

Everything in this repository is licensed under the Pare Sustainable Use License
(see [LICENSE](LICENSE)). That is the whole single-company bookkeeping product:
the double-entry ledger on the BAS chart, invoicing, moms (incl. förvärvsmoms),
SIE 4 import/export, bank reconciliation, receipts, the reports, the MCP, and both
privacy layers (at-rest envelope encryption + the MCP-boundary Shield
tokenization).

This is a [fair-code](https://faircode.io) license, not an OSI "open source"
license. The one limit: you may not resell Pare or run it as a hosted service for
third parties (a competing "Pare cloud"). Self-hosting, internal commercial use,
and keeping the books for your own clients (as an accountant or bookkeeper) are all
expressly fine.

## Enterprise overlay (not in this repository)

A separate commercial `pro` build overlay (Go build tag `pro`) is held back for
the hosted SaaS and features that only make sense at multi-tenant scale: multiple
companies / agency console, live PSD2 bank feeds, Peppol e-invoicing, payroll
(AGI), and K2/K3 årsredovisning. Those live outside this repo, behind the build
tag. The core builds and runs fully on its own without them.

## Commercial license

If you want to do something the Sustainable Use License does not permit (for
example, offering Pare as a hosted service to third parties, or embedding it in a
closed product), a commercial license is available at licensing@brightinteraction.com.
