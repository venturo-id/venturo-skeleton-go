#!/usr/bin/env bash
# Fails when the branch changes non-test Go code without touching CHANGELOG.md.
# Every change to the code gets an entry under "## [Unreleased]".
#
# Usage: scripts/check-changelog.sh [BASE]
set -euo pipefail

changed="$("$(dirname "$0")/changed-files.sh" "$@")"

code="$(grep -E '\.go$' <<<"$changed" | grep -vE '_test\.go$' || true)"
if [ -z "$code" ]; then
  echo "check-changelog: no Go code changed"
  exit 0
fi

if ! grep -qx 'CHANGELOG.md' <<<"$changed"; then
  echo "check-changelog: Go code changed without a CHANGELOG.md entry:" >&2
  sed 's/^/  /' <<<"$code" >&2
  echo "Add an entry under '## [Unreleased]' in CHANGELOG.md." >&2
  exit 1
fi

if ! grep -q '^## \[Unreleased\]' CHANGELOG.md; then
  echo "check-changelog: CHANGELOG.md has no '## [Unreleased]' section" >&2
  exit 1
fi

echo "check-changelog: ok"
