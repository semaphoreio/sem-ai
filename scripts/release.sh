#!/usr/bin/env bash
# Bump plugin manifests, commit, and tag a new release.
#
# Usage:
#   scripts/release.sh 0.1.8
#   scripts/release.sh v0.1.8                # leading 'v' allowed
#   scripts/release.sh --dry-run 0.1.8       # validate + print actions, no changes
#
# Does NOT push — prints the push commands instead so release stays
# gated on a deliberate human action.

set -euo pipefail

MARKETPLACE=".claude-plugin/marketplace.json"
PLUGIN="assets/plugin/plugin.json"

dry_run=0
if [[ "${1:-}" == "--dry-run" ]]; then
  dry_run=1
  shift
fi

if [[ $# -lt 1 || -z "${1:-}" ]]; then
  echo "ERROR: version required" >&2
  echo "Usage: scripts/release.sh [--dry-run] <X.Y.Z>" >&2
  exit 2
fi

version="${1#v}"

run() {
  if [[ "$dry_run" -eq 1 ]]; then
    echo "DRY-RUN: $*"
  else
    "$@"
  fi
}

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "ERROR: version must match X.Y.Z (got '$version')" >&2
  exit 2
fi

if ! command -v yq >/dev/null 2>&1; then
  echo "ERROR: yq required (brew install yq)" >&2
  exit 2
fi

if [[ ! -f "$MARKETPLACE" || ! -f "$PLUGIN" ]]; then
  echo "ERROR: run from repo root — missing $MARKETPLACE or $PLUGIN" >&2
  exit 2
fi

if [[ "$dry_run" -eq 0 ]]; then
  if [[ -n "$(git status --porcelain)" ]]; then
    echo "ERROR: working tree dirty — commit or stash first" >&2
    exit 1
  fi

  branch="$(git rev-parse --abbrev-ref HEAD)"
  if [[ "$branch" != "main" ]]; then
    echo "ERROR: must run on main branch (currently on $branch)" >&2
    exit 1
  fi
fi

if [[ -n "$(git tag -l "v$version")" ]]; then
  echo "ERROR: tag v$version already exists locally" >&2
  exit 1
fi

run yq -i -o json ".plugins[0].version = \"$version\"" "$MARKETPLACE"
run yq -i -o json ".version = \"$version\"" "$PLUGIN"

if [[ "$dry_run" -eq 0 ]]; then
  git --no-pager diff --stat "$MARKETPLACE" "$PLUGIN"
fi
run git add "$MARKETPLACE" "$PLUGIN"
run git commit -m "chore(release): bump plugin manifests to v$version"
run git tag -a "v$version" -m "v$version"

echo
if [[ "$dry_run" -eq 1 ]]; then
  echo "DRY-RUN: no files changed, no commit, no tag."
  echo "Re-run without --dry-run to apply."
else
  echo "Bumped + committed + tagged v$version locally. To publish:"
  echo "  git push origin main && git push origin v$version"
fi
