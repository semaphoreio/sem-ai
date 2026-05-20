#!/usr/bin/env bash
# Bump plugin manifests, commit, and tag a new release.
#
# Usage:
#   scripts/release.sh 0.1.8
#   scripts/release.sh v0.1.8   # leading 'v' allowed
#
# Does NOT push — prints the push commands instead so release stays
# gated on a deliberate human action.

set -euo pipefail

MARKETPLACE=".claude-plugin/marketplace.json"
PLUGIN="assets/plugin/plugin.json"

if [[ $# -lt 1 || -z "${1:-}" ]]; then
  echo "ERROR: version required" >&2
  echo "Usage: scripts/release.sh <X.Y.Z>" >&2
  exit 2
fi

version="${1#v}"

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

if [[ -n "$(git status --porcelain)" ]]; then
  echo "ERROR: working tree dirty — commit or stash first" >&2
  exit 1
fi

branch="$(git rev-parse --abbrev-ref HEAD)"
if [[ "$branch" != "main" ]]; then
  echo "ERROR: must run on main branch (currently on $branch)" >&2
  exit 1
fi

if [[ -n "$(git tag -l "v$version")" ]]; then
  echo "ERROR: tag v$version already exists locally" >&2
  exit 1
fi

yq -i -o json ".plugins[0].version = \"$version\"" "$MARKETPLACE"
yq -i -o json ".version = \"$version\"" "$PLUGIN"

git --no-pager diff --stat "$MARKETPLACE" "$PLUGIN"
git add "$MARKETPLACE" "$PLUGIN"
git commit -m "chore(release): bump plugin manifests to v$version"
git tag -a "v$version" -m "v$version"

echo
echo "Bumped + committed + tagged v$version locally. To publish:"
echo "  git push origin main && git push origin v$version"
