# Releasing Pare as a public `go install` tool

Pare's module path is `github.com/bright-interaction/pare`, but the code lives in
the private `bright-interaction/automations` monorepo under `pare/`. Go resolves a
module from a repo whose path matches the module path, so a public release is:
**mirror the `pare/` subtree to its own repo `github.com/bright-interaction/pare`,
then tag a version.**

Pare is open core (see [../LICENSING.md](../LICENSING.md)): the mirror carries the
whole `pare/` tree under the Pare Sustainable Use License. The commercial pro overlay (multi-company, PSD2
bank feeds, Peppol, payroll, hosted SaaS) lives outside this repo behind the `pro`
build tag, so nothing is stripped for licensing. The split script strips only the
estate deploy compose and redacts internal infra hostnames from history
(`scripts/split-public-repo.sh`).

This is an outward, hard-to-reverse step (it exposes the source publicly), so it is
a deliberate operator action, not part of `git psync`. It requires `git-filter-repo`
and `gitleaks` on PATH.

## One-time: create the public repo and seed it

1. Dry run first (safe, no push): `./scripts/split-public-repo.sh`. It subtree-splits,
   strips/redacts, build-checks and gitleaks-scans the filtered tree, then prints what
   it WOULD push. Get this green before step 2.
2. Create the public repo (outward):
   ```
   gh repo create bright-interaction/pare --public \
     --description "Lean, sovereign Swedish bookkeeping + invoicing with an AI (MCP) that never sees your counterparties. Fair-code."
   ```
3. Mirror and push:
   ```
   ./scripts/split-public-repo.sh --push
   ```
4. Verify the install resolves before tagging:
   ```
   go install github.com/bright-interaction/pare/cmd/server@latest
   ```

## Cut a version

Tags live on the PUBLIC repo (a monorepo tag cannot satisfy a differing module path):

```
# in a clone of github.com/bright-interaction/pare:
git tag v0.1.0
git push origin v0.1.0
```

Suggested first tag: **v0.1.0** (the ledger, invoicing, moms, SIE, bank and MCP
surface are stable in shape, not yet frozen).

## Each subsequent release

```
./scripts/split-public-repo.sh --push      # re-mirror the latest pare/ subtree
# then tag the new version on the public repo
```

## What stays private

The hosted SaaS, ops scripts, `.env`s and the rest of the monorepo never leave
`bright-interaction/automations`. Only the `pare/` subtree is mirrored, minus the
estate `docker-compose.yml`. The split script's gitleaks gate enforces that no
secret was ever committed under `pare/` before any public push.
