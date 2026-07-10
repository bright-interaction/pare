#!/usr/bin/env bash
# Produce the public fair-code mirror of Pare at github.com/bright-interaction/pare,
# so `go install github.com/bright-interaction/pare/cmd/server@latest` resolves.
#
# Pare is open core: the whole pare/ tree ships in the mirror under the Pare
# Sustainable Use License (fair-code: self-host free, no reselling as a service). The
# commercial pro overlay (multi-company, PSD2 bank feeds, Peppol, payroll, hosted
# SaaS) lives OUTSIDE this repo behind the `pro` build tag, so there is no pro code
# to strip here (see LICENSING.md). This script strips only the estate deploy
# compose (which names the house proxy network) and redacts internal infra
# hostnames from all history, then secret-scans and build-checks before any push.
#
# Safe by default: with no --push it produces + checks the filtered tree and prints
# what it WOULD push. --push performs the outward mirror (requires the public repo
# to exist: gh repo create bright-interaction/pare --public).
#
# Pattern (single-branch split-clone + gitleaks gate) mirrors mesh/reactor/flare;
# see the Hive gotcha "mesh-mirror-split-clone-drags-in-monorepo-branch-secrets".
set -euo pipefail

PUSH=0
REMOTE_URL="git@github.com:bright-interaction/pare.git"
PREFIX="pare"
SPLIT_BRANCH="pare-public-split"

# Internal-ONLY files (not app code): stripped from the mirror's entire history.
# Paths are relative to pare/ (the subtree split strips the prefix). The top-level
# docker-compose.yml is the ESTATE deploy config (external web-proxy network);
# public self-hosters use deploy/docker-compose.yml instead, which is generic.
STRIP_PATHS=(
  docker-compose.yml
)

for arg in "$@"; do
  case "$arg" in
    --push) PUSH=1 ;;
    --remote=*) REMOTE_URL="${arg#--remote=}" ;;
    -h|--help) echo "usage: $0 [--push] [--remote=git@github.com:org/repo.git]"; exit 0 ;;
    *) echo "unknown arg: $arg" >&2; exit 2 ;;
  esac
done

command -v git-filter-repo >/dev/null 2>&1 || {
  echo "error: git-filter-repo is required (pip install git-filter-repo)." >&2; exit 1; }

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"
[ -d "$PREFIX" ] || { echo "error: $PREFIX/ not found at $ROOT" >&2; exit 1; }

# Coarse pre-flight secret guard on the subtree history (defense before the
# authoritative gitleaks scan on the filtered clone below). The value class
# excludes '.' so dotted Go selectors / SQL columns break into sub-16-char tokens
# instead of false-tripping; documented placeholders are filtered out. A real
# high-entropy credential is a contiguous >=16 run without those markers and still
# trips the guard; dotted secrets (JWTs) are caught by the gitleaks gate below.
if git log -p -- "$PREFIX/" \
  | grep -iE '(api[_-]?key|secret|password|bearer|private[_-]?key)[[:space:]]*[:=][[:space:]]*["'"'"']?[A-Za-z0-9/_+-]{16,}' \
  | grep -ivE 'changeme|change[_-]?me|your_|_here|example|redacted|placeholder|xxxx|base64_32_bytes|min_16_char' \
  | grep -q .; then
  echo "REFUSING: a possible secret appears in $PREFIX/ history. Audit before any push:" >&2
  echo "  git log -p -- $PREFIX/ | grep -iE 'key|secret|token|password'" >&2
  exit 1
fi

echo "Splitting $PREFIX/ subtree (history-preserving) into $SPLIT_BRANCH ..."
git branch -D "$SPLIT_BRANCH" >/dev/null 2>&1 || true
git subtree split --prefix="$PREFIX" -b "$SPLIT_BRANCH"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
CLONE="$WORK/pare-public"
# --single-branch + --no-tags: the throwaway clone holds ONLY the disjoint pare
# subtree history, never the monorepo's other branches (which carry unrelated
# project CI secrets). The clone == the publish payload, which makes the gitleaks
# scan below authoritative. file:// disables the hardlink path.
echo "Cloning $SPLIT_BRANCH -> $CLONE (single-branch) ..."
git clone --quiet --single-branch --no-tags --branch "$SPLIT_BRANCH" "file://$ROOT" "$CLONE"

if [ "${#STRIP_PATHS[@]}" -gt 0 ]; then
  FR_ARGS=(); for p in "${STRIP_PATHS[@]}"; do FR_ARGS+=(--path "$p"); done
  echo "Stripping internal-only paths from all history: ${STRIP_PATHS[*]}"
  ( cd "$CLONE" && git filter-repo --force --invert-paths "${FR_ARGS[@]}" )
fi

# Redact internal infra references from ALL history (file contents + commit
# messages). Distinctive tokens only, so a literal global replace is safe.
REDACT="$WORK/redactions.txt"
{
  echo 'host==>host'
  echo 'web-proxy==>web-proxy'
} > "$REDACT"
echo "Redacting internal infra hostnames from all history ..."
( cd "$CLONE" && git filter-repo --force --replace-text "$REDACT" --replace-message "$REDACT" )

# Defense in depth: fail if a stripped path survived.
for p in "${STRIP_PATHS[@]}"; do
  [ -e "$CLONE/$p" ] && { echo "REFUSING: stripped path '$p' still present." >&2; exit 1; }
done

echo "Build-checking the mirror ..."
if command -v go >/dev/null 2>&1; then
  ( cd "$CLONE" && go build ./... ) && echo "  builds standalone: OK"
  ( cd "$CLONE" && go test -run='^$' ./... >/dev/null ) && echo "  tests compile: OK"
else
  echo "  (go not found; skipping build check)" >&2
fi

# Authoritative secret scan: the single-branch clone IS the publish payload.
if command -v gitleaks >/dev/null 2>&1; then
  echo "Scanning mirror history for secrets (gitleaks) ..."
  if ! ( cd "$CLONE" && gitleaks detect --source . --config .gitleaks.toml --no-banner --redact >/dev/null 2>&1 ); then
    echo "REFUSING: gitleaks found a secret in the mirror history:" >&2
    ( cd "$CLONE" && gitleaks detect --source . --config .gitleaks.toml --no-banner --redact ) >&2 || true
    exit 1
  fi
  echo "  no secrets in mirror history: OK"
else
  echo "  WARNING: gitleaks not installed; the secret-scan gate is SKIPPED." >&2
  echo "  Install it before pushing: brew install gitleaks" >&2
  [ "$PUSH" -eq 1 ] && { echo "REFUSING to --push without the gitleaks gate." >&2; exit 1; }
fi

if [ "$PUSH" -eq 0 ]; then
  echo; echo "DRY RUN. Filtered mirror ready at: $CLONE"
  echo "Would push its HEAD -> $REMOTE_URL main"
  echo "Re-run with --push once the public repo exists (gh repo create bright-interaction/pare --public)."
  trap - EXIT  # keep $WORK so the operator can inspect the dry-run tree
  exit 0
fi

echo "Pushing filtered mirror -> $REMOTE_URL main ..."
( cd "$CLONE" && git push "$REMOTE_URL" HEAD:main )
echo "Done. Cleanup: git branch -D $SPLIT_BRANCH"
