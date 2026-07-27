#!/usr/bin/env bash
# SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
# Copyright (c) Bright Interaction
#
# Every check CI runs, in one script you can also run yourself:
#
#   bash scripts/ci.sh
#
# Run it before opening a pull request and CI will not tell you anything new.
#
# WHY THE LOGIC LIVES HERE AND NOT IN .github/workflows/ci.yml
#
# Two reasons, and the second is not obvious.
#
# 1. A contributor can run it. Checks that only exist inside a workflow file are
#    checks you cannot reproduce locally, so you discover them after pushing.
#
# 2. It keeps .github/workflows/ci.yml STABLE, which is what makes automated
#    publishing possible at all. This repository is a filtered mirror of a private
#    monorepo, published by a CI job authenticated with a repository deploy key.
#    GitHub does not allow a deploy key (or an OAuth token without the `workflow`
#    scope) to create or update anything under .github/workflows/. So a check that
#    lived in the workflow file would break the publish at the FINAL push, after
#    every gate had already passed, with a raw git error that reads like a mystery.
#    With the steps in this script, changing a check touches scripts/ci.sh (an
#    ordinary file the deploy key may write) and the workflow file stays untouched,
#    so the publish goes through.
#
# The consequence to remember: editing .github/workflows/ci.yml is now a rare,
# deliberate act (an action version bump, a trigger change) that needs a push from a
# credential with the `workflow` scope. Editing THIS file needs nothing special.
set -uo pipefail

cd "$(git rev-parse --show-toplevel 2>/dev/null)" 2>/dev/null || true
[ -f go.mod ] || cd pare 2>/dev/null || true
[ -f go.mod ] || { echo "error: run this from the Pare module root (the directory holding go.mod)" >&2; exit 1; }

FAILED=()
SKIPPED=()
# Exit code 99 means "this check did not run". It is distinct from pass and from fail
# on purpose: a skipped security scan that prints ok is worse than no scan at all,
# because it looks like evidence.
step() {
  local name="$1" rc; shift
  printf '\n=== %s ===\n' "$name"
  "$@"; rc=$?
  case "$rc" in
    0)  printf 'ok: %s\n' "$name" ;;
    99) printf 'SKIPPED: %s\n' "$name"; SKIPPED+=("$name") ;;
    *)  printf 'FAIL: %s\n' "$name" >&2; FAILED+=("$name") ;;
  esac
}

# Are we in the published mirror, or in the private monorepo the mirror is cut from?
# scripts/split-public-repo.sh strips exactly one path from the mirror's history: the
# top-level docker-compose.yml, which is the ESTATE deploy config naming the house
# proxy network (public self-hosters get the generic deploy/docker-compose.yml
# instead). So that file is present upstream and absent downstream, which makes it a
# reliable marker. The secret scan below has to mean different things in the two
# places, and getting that wrong makes it either useless upstream or toothless
# downstream.
IN_MIRROR=1
[ -f docker-compose.yml ] && IN_MIRROR=0

# gofmt exits 0 whether or not it found anything; the FILE LIST is the result, so the
# status tells you nothing and has to be ignored in favour of the output.
gofmt_check() {
  local unformatted
  unformatted="$(gofmt -l . 2>/dev/null)"
  if [ -n "$unformatted" ]; then
    echo "These files are not gofmt-clean:" >&2
    echo "$unformatted" >&2
    echo "Fix with: gofmt -w ." >&2
    return 1
  fi
  echo "every Go file is gofmt-clean"
}

# RUN the tests, do not merely compile them. `go test ./... -run='^$'` compiles every
# _test.go and executes none while still printing ok, and a gate labelled "tests
# compile" was read as "tests pass" for months in a sibling repo. -count=1 disables the
# test cache so a green run means this tree is green now, not that it was green once.
#
# -timeout is per package. The default 10m is close for internal/store, whose suite
# takes ~4 minutes against a container Postgres on a laptop, so give it room rather
# than have a slow runner report a fake deadlock.
run_tests() {
  go test ./... -count=1 -timeout 20m
}

# The half of the suite that needs Postgres SELF-SKIPS, silently, and that is exactly
# the shape of failure that looks like success: internal/testdb calls t.Skip when
# PARE_TEST_DATABASE_URL is unset, and `go test` still prints ok for the package. 32 of
# the 72 tests in this repo go that way, all of internal/store and internal/mcp: the
# ledger, invoices, the audit hash chain, counterparty erasure, and every MCP tool that
# writes bookkeeping. So report that separately instead of letting one "ok" cover both
# cases. It runs BEFORE the test gate so a wrong DSN fails in seconds rather than
# surfacing as an unrelated-looking store failure minutes in.
postgres_suites() {
  if [ -z "${PARE_TEST_DATABASE_URL:-}" ]; then
    echo "PARE_TEST_DATABASE_URL is unset, so every database-backed test will skip"
    echo "itself in the test gate below (32 of 72, all of internal/store and"
    echo "internal/mcp) while go test still prints ok for those packages. That gate is"
    echo "honest about what it ran; this one exists to say what it did not."
    echo
    echo "To run them, point it at any Postgres you can create databases on:"
    echo
    echo "  docker run --rm -d --name pare-ci-pg -p 5432:5432 \\"
    echo "    -e POSTGRES_USER=pare -e POSTGRES_PASSWORD=pare -e POSTGRES_DB=pare postgres:16-alpine"
    echo "  export PARE_TEST_DATABASE_URL='postgres://pare:pare@localhost:5432/pare?sslmode=disable'"
    echo
    echo "internal/testdb derives a separate pare_<suffix> database per test package"
    echo "from that DSN, so nothing already in there is touched."
    return 99
  fi
  # Set. Prove the DSN reaches a server these suites can actually use (connect, CREATE
  # DATABASE, migrate) rather than trusting the string.
  echo "PARE_TEST_DATABASE_URL is set; running one database-backed test to prove it"
  echo "reaches a usable server before committing to the full run."
  go test ./internal/store -count=1 -run TestSyncChartBackfills
}

# Everything this repo publishes is under the Pare Sustainable Use License, and the
# SPDX header is how a reader (and a downstream packager) knows it. A file without one
# is unlicensed as far as anyone downstream can tell. This is not hypothetical: three
# files in internal/flarereport reached the published mirror with no header at all,
# because nothing was checking.
#
# Scope is Go source, minus internal/db/generated. Contributions land in Go, and sqlc
# rewrites everything under internal/db/generated wholesale from internal/db/queries,
# so a header added there disappears at the next `sqlc generate`. The HTML templates,
# the goose migrations and the query files carry no headers today either; bringing
# them in is a real change to ~60 files and belongs in its own commit, not smuggled in
# behind a CI gate.
license_headers() {
  local missing
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
    echo "not a git checkout, so there is no tracked-file list to walk"
    return 99
  }
  missing=$(git ls-files '*.go' | grep -v '^internal/db/generated/' | while read -r f; do
    awk 'NR<=5 && /SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License/ { found=1; exit } END { exit !found }' "$f" \
      || printf '%s\n' "$f"
  done)
  if [ -n "$missing" ]; then
    echo "These Go files lack the Pare SPDX header in their first 5 lines:" >&2
    echo "$missing" >&2
    echo "Add it above the package clause:" >&2
    echo "  // SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License" >&2
    echo "  // Copyright (c) Bright Interaction" >&2
    return 1
  fi
  echo "every hand-written Go file carries the Pare SPDX header"
}

# The scanners run from a local binary if you have one, and are installed on demand
# otherwise, which needs network. Set PARE_CI_SKIP_SCANNERS=1 to skip them when
# offline. They then report SKIPPED rather than passing quietly, because a security
# scan that says ok without running is worse than no scan.
scan() {
  local name="$1" mod="$2" bin; shift 2
  if [ "${PARE_CI_SKIP_SCANNERS:-0}" = "1" ]; then
    echo "not run: PARE_CI_SKIP_SCANNERS=1"
    return 99
  fi
  bin="$(command -v "$name" 2>/dev/null)"
  if [ -z "$bin" ]; then
    go install "$mod" >/dev/null 2>&1 || { echo "could not install $name (offline?)" >&2; return 1; }
    bin="$(go env GOPATH)/bin/$name"
  fi
  "$bin" "$@"
}

step "build"                    go build ./...
step "vet"                      go vet ./...
step "gofmt"                    gofmt_check
step "database-backed suites"   postgres_suites
step "tests (RUN, not just compiled)" run_tests
step "license headers"          license_headers
# .gitleaks.toml allowlists the change-me placeholders in .env.example, so pass it or
# the config examples trip the scan and everyone learns to ignore the result.
#
# In the mirror, every commit in the history is this project's, so scanning the full
# history is exactly right and is the last gate before a credential becomes
# world-readable. Upstream, the history belongs to a whole monorepo of unrelated
# projects and gitleaks would walk all of it, so there the working tree is what
# matters; the publish pipeline scans the filtered payload's own full history
# separately before it pushes (scripts/split-public-repo.sh).
if [ "$IN_MIRROR" = "1" ]; then
  step "secret scan (full history)" \
    scan gitleaks github.com/zricethezav/gitleaks/v8@latest \
    detect --source . --config .gitleaks.toml --no-banner --redact
else
  step "secret scan (working tree; the publish pipeline scans the filtered history)" \
    scan gitleaks github.com/zricethezav/gitleaks/v8@latest \
    detect --source . --config .gitleaks.toml --no-git --no-banner --redact
fi
step "vulnerability scan" scan govulncheck golang.org/x/vuln/cmd/govulncheck@latest ./...

printf '\n'
if [ ${#SKIPPED[@]} -ne 0 ]; then
  printf 'ci: %d check(s) DID NOT RUN: %s\n' "${#SKIPPED[@]}" "${SKIPPED[*]}" >&2
fi
if [ ${#FAILED[@]} -ne 0 ]; then
  # Report EVERY failure, not just the first. A run that stops at the first problem
  # costs a full round trip per issue.
  printf 'ci: %d check(s) failed: %s\n' "${#FAILED[@]}" "${FAILED[*]}" >&2
  exit 1
fi
if [ ${#SKIPPED[@]} -ne 0 ]; then
  echo "ci: every check that RAN passed, but some did not run (see above)"
  exit 0
fi
echo "ci: all checks passed"
