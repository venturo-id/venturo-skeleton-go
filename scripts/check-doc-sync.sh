#!/usr/bin/env bash
# Fails when the branch changes the API surface or the schema without the
# matching documents, and when a changed API contract keeps its old
# "**Last Updated:**" line:
#   internal/modules/<domain>/**/handler|dto/**  ->  docs/api-contract/<domain>/**
#   other handler/dto, internal/router/**        ->  docs/api-contract/**
#   internal/database/migrations/<module>/**     ->  docs/erd/<module>.mmd
# Test files do not count. A change that leaves the contract as it is (a pure
# refactor) runs with DOC_SYNC_SKIP=1; CI sets it from the PR label
# "no-doc-change".
#
# Usage: scripts/check-doc-sync.sh [BASE]
set -euo pipefail

if [ "${DOC_SYNC_SKIP:-}" = "1" ] || [ "${DOC_SYNC_SKIP:-}" = "true" ]; then
  echo "check-doc-sync: skipped (DOC_SYNC_SKIP)"
  exit 0
fi

here="$(dirname "$0")"
changed="$("$here/changed-files.sh" "$@")"
merge_base="$("$here/changed-files.sh" --merge-base "$@")"
missing=()

touched() { grep -qE "$1" <<<"$changed"; }

while IFS= read -r file; do
  [ -z "$file" ] && continue
  case "$file" in
    *_test.go) continue ;;
  esac
  if [[ "$file" =~ ^internal/database/migrations/([^/]+)/ ]]; then
    erd="docs/erd/${BASH_REMATCH[1]}.mmd"
    touched "^${erd//./\\.}$" || missing+=("$file -> $erd")
  elif [[ "$file" =~ ^internal/modules/([^/]+)/.*/(handler|dto)/ ]]; then
    contract="docs/api-contract/${BASH_REMATCH[1]}/"
    touched "^$contract" || missing+=("$file -> $contract")
  elif [[ "$file" =~ ^internal/router/ || "$file" =~ (^|/)(handler|dto)/ ]]; then
    touched '^docs/api-contract/' || missing+=("$file -> docs/api-contract/")
  fi
done <<<"$changed"

stale=()
while IFS= read -r doc; do
  if [ -z "$doc" ] || [ ! -f "$doc" ]; then
    continue
  fi
  if git ls-files --error-unmatch "$doc" >/dev/null 2>&1; then
    git diff "$merge_base" -- "$doc" | grep -q '^+\*\*Last Updated:\*\*' || stale+=("$doc")
  else
    grep -q '^\*\*Last Updated:\*\*' "$doc" || stale+=("$doc")
  fi
done < <(grep -E '^docs/api-contract/.*\.md$' <<<"$changed" || true)

if [ ${#missing[@]} -gt 0 ]; then
  echo "check-doc-sync: changed without the matching document:" >&2
  printf '  %s\n' "${missing[@]}" >&2
fi
if [ ${#stale[@]} -gt 0 ]; then
  echo "check-doc-sync: contract changed but its '**Last Updated:**' line was not updated:" >&2
  printf '  %s\n' "${stale[@]}" >&2
fi
if [ ${#missing[@]} -gt 0 ] || [ ${#stale[@]} -gt 0 ]; then
  echo "See .claude/rules/api-docs.md. A pure refactor that leaves the contract as is: DOC_SYNC_SKIP=1 (PR label no-doc-change)." >&2
  exit 1
fi

echo "check-doc-sync: ok"
