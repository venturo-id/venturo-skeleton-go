#!/usr/bin/env bash
# Prints the files this branch changes against BASE, one per line: committed
# changes since the merge base, plus staged, unstaged and untracked files, so
# `make check` sees the same change before and after the commit.
#
# Usage: scripts/changed-files.sh [--merge-base] [BASE]
#   BASE defaults to $BASE, then origin/HEAD.
#   --merge-base prints the merge-base commit instead of the files.
set -euo pipefail

print_merge_base=false
if [ "${1:-}" = "--merge-base" ]; then
  print_merge_base=true
  shift
fi

base="${1:-${BASE:-}}"
if [ -z "$base" ]; then
  base="$(git rev-parse --abbrev-ref origin/HEAD 2>/dev/null || true)"
fi
if [ -z "$base" ] || ! git rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
  echo "changed-files: base '$base' not found; pass BASE=<branch>, e.g. BASE=origin/production-simple" >&2
  exit 2
fi

merge_base="$(git merge-base "$base" HEAD)"
if $print_merge_base; then
  echo "$merge_base"
  exit 0
fi

{
  git diff --name-only "$merge_base"
  git ls-files --others --exclude-standard
} | sort -u
