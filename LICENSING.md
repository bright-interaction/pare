# Licensing and open core

Pare is open core.

## Core (this repository) - AGPL-3.0-or-later

Everything in this repository is licensed under the GNU Affero General Public
License v3.0 or later (see [LICENSE](LICENSE)). That is the whole single-company
bookkeeping product: the double-entry ledger on the BAS chart, invoicing, moms
(incl. förvärvsmoms), SIE 4 import/export, bank reconciliation, receipts, the
reports, the MCP, and both privacy layers (at-rest envelope encryption + the
MCP-boundary Shield tokenization).

AGPL means: if you run a modified Pare as a network service, you must offer your
users the modified source. That is deliberate. It keeps Pare and its improvements
open and stops a competitor from running a closed, rehosted fork.

## Pro overlay (not in this repository) - commercial

A separate commercial `pro` build overlay (Go build tag `pro`) is reserved for the
hosted SaaS and features that only make sense at multi-tenant scale: multiple
companies / agency console, live PSD2 bank feeds, Peppol e-invoicing, payroll
(AGI), and K2/K3 årsredovisning. Those live outside this repo, behind the build
tag. The core builds and runs fully on its own without them.

## Commercial license

If you cannot accept the AGPL (for example, you want to embed Pare in a closed
product), a commercial license is available. Contact Bright Interaction.
