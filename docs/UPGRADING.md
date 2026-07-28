# Upgrading Pare

Notes for operators upgrading an existing install. Only entries that need action
are listed. Newest first.

## 2026-07-28: dot-grouped amounts in a bank CSV imported 1000x to 1000000x too small

**What was wrong.** The single amount parser shared by the camt.053 and CSV
importers picked whichever of `.` or `,` occurred last as the decimal separator
and then split on the FIRST one. A Swedish CSV cell that groups thousands with a
dot therefore lost every group after the first:

| amount cell | imported as | should have been |
| --- | --- | --- |
| `12.500` | 12,50 kr | 12 500 kr |
| `999.500` | 999,50 kr | 999 500 kr |
| `1.500.000` | 1,50 kr | 1 500 000 kr |
| `1.234.567` | 1,23 kr | 1 234 567 kr |

There was no error and no warning: the row imported with a wrong amount, so it
silently failed to match its invoice or matched a much smaller one. camt.053
imports were never affected, and neither were space-grouped (`12 500,00`) or
plain (`12500,00`) CSV amounts.

**Who is affected.** Anyone who imported a bank CSV whose amounts use `.` or `,`
as a thousands separator, on a build from before 2026-07-28. The hosted instance
at pare.brightinteraction.com had 0 rows in `bank_transactions` (and 0 companies)
when this was fixed, so nothing there needed repair.

**How to check.** There is no server-side way to tell a corrupted row from a
correct one, so this is a comparison against the original statement:

```sql
-- every bank line imported before you deployed this version
SELECT id, txn_date, amount_ore, ref, status, created_at
FROM bank_transactions
WHERE created_at < '2026-07-28'   -- your upgrade timestamp
ORDER BY txn_date;
```

Compare `amount_ore` (öre, so 1 250 000 is 12 500 kr) against the statement the
rows came from. If your CSV amounts never contained a `.` or `,` as a grouping
separator, you are not affected and there is nothing to do.

**How to repair a wrong row.**

1. Not yet booked (`status` is `unmatched` or `ignored`): re-import the same
   statement file. The corrected line imports as a NEW row, because the dedup
   fingerprint is a hash over the amount, so it does not collide with the stale
   one and is not skipped as a duplicate. Then mark the stale row Ignorera in
   `/bank` so it stops offering itself for reconciliation.
2. Already booked (`status` is `booked`): do not edit the amount, in the database
   or anywhere else. The verifikat is immutable on purpose and its hash is part of
   the audit chain. Post a rättelseverifikat (reversing entry) for the wrong
   verifikat and book the payment again from the corrected bank line, which is the
   normal Swedish correction procedure.

**Why there is no automatic migration.** The corruption is not invertible and not
detectable after the fact. `12.500` imported as 1250 öre, which is exactly what a
genuine 12,50 kr line looks like, and the uploaded statement file is not stored
server-side (the importer parses it in memory), so nothing on the server can tell
the two apart. A migration that guessed would corrupt correct rows, and rewriting
an amount that has already been booked would break the append-only ledger.

**Behaviour change to expect.** The parser is now fail-closed: an amount cell that
is not unambiguously a number is rejected and the row is skipped instead of
importing a made-up amount. Cells that used to import a fabricated value and are
now skipped include `1..5`, `1.500.00`, `1.2.3`, a dotted date that landed in the
amount column (`05.03.2026`), and anything that overflows int64. One genuinely
ambiguous shape is also skipped: a single comma with exactly three digits after
it (`1,234`), which is a three-decimal amount in Swedish and a thousands group
elsewhere. Write it as `1,23` or `1.234`, or export camt.053, which has no
ambiguity by construction.

One sign case also changed: an amount cell with the currency in front of the
minus (`SEK -1234,00`) used to import as a positive amount, because the currency
trim removed the minus with it. It now imports as money out. If you imported CSVs
in that shape, check those lines with the query above too.
