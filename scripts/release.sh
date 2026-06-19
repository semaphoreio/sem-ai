#!/usr/bin/env bash
# Bump plugin manifests and commit, on a release branch, for the PR-based flow.
#
# Usage:
#   scripts/release.sh 0.1.8                  # bump + commit on the current branch
#   scripts/release.sh v0.1.8                 # leading 'v' allowed
#   scripts/release.sh --dry-run 0.1.8        # validate + print actions, no changes
#
# `main` is protected (PR required, linear history), so a release can no longer
# be pushed straight to main. This script does ONE half of the flow: bump the
# manifests and commit them on a feature branch. The other half — tagging the
# squashed bump commit once the PR merges — is scripts/tag-release.sh.
#
# Full flow:
#   1. git checkout -b mk/sem-ai/release-vX.Y.Z origin/main
#   2. scripts/release.sh X.Y.Z          # this script: bump + commit
#   3. git push -u origin HEAD && gh pr create ...   # open PR, squash-merge
#   4. git checkout main && git pull
#   5. scripts/tag-release.sh X.Y.Z      # tag the merged bump + print push cmd
#
# Bumps every file that carries the plugin version:
#   .claude-plugin/marketplace.json        (.plugins[0].version)
#   assets/plugin/plugin.json              (.version)
#   assets/plugin/.codex-plugin/plugin.json (.version)
#   mcpb/manifest.json                     (.version)
#
# Does NOT push and does NOT tag — release stays gated on deliberate human steps.

set -euo pipefail

MARKETPLACE=".claude-plugin/marketplace.json"
PLUGIN="assets/plugin/plugin.json"
CODEX="assets/plugin/.codex-plugin/plugin.json"
MCPB="mcpb/manifest.json"

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

for f in "$MARKETPLACE" "$PLUGIN" "$CODEX" "$MCPB"; do
  if [[ ! -f "$f" ]]; then
    echo "ERROR: run from repo root — missing $f" >&2
    exit 2
  fi
done

if [[ "$dry_run" -eq 0 ]]; then
  if [[ -n "$(git status --porcelain)" ]]; then
    echo "ERROR: working tree dirty — commit or stash first" >&2
    exit 1
  fi

  branch="$(git rev-parse --abbrev-ref HEAD)"
  if [[ "$branch" == "main" ]]; then
    echo "ERROR: main is protected — bump on a release branch, not main" >&2
    echo "  git checkout -b mk/sem-ai/release-v$version origin/main" >&2
    exit 1
  fi
fi

if [[ -n "$(git tag -l "v$version")" ]]; then
  echo "ERROR: tag v$version already exists locally" >&2
  exit 1
fi

run yq -i -o json ".plugins[0].version = \"$version\"" "$MARKETPLACE"
run yq -i -o json ".version = \"$version\"" "$PLUGIN"
run yq -i -o json ".version = \"$version\"" "$CODEX"
run yq -i -o json ".version = \"$version\"" "$MCPB"

if [[ "$dry_run" -eq 0 ]]; then
  git --no-pager diff --stat "$MARKETPLACE" "$PLUGIN" "$CODEX" "$MCPB"
fi
run git add "$MARKETPLACE" "$PLUGIN" "$CODEX" "$MCPB"
run git commit -m "chore(release): bump plugin manifests to v$version"

echo
if [[ "$dry_run" -eq 1 ]]; then
  echo "DRY-RUN: no files changed, no commit."
  echo "Re-run without --dry-run to apply."
else
  echo "Bumped + committed v$version on branch $(git rev-parse --abbrev-ref HEAD). Next:"
  echo "  git push -u origin HEAD && gh pr create --fill"
  echo "  # after the PR squash-merges:"
  echo "  git checkout main && git pull"
  echo "  scripts/tag-release.sh $version"
fi
