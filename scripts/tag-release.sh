#!/usr/bin/env bash
# Tag a merged release bump and print the push command that publishes it.
#
# Second half of the PR-based release flow (first half: scripts/release.sh).
# Run on main AFTER the manifest-bump PR has squash-merged.
#
# Usage:
#   scripts/tag-release.sh 0.1.8             # tag v0.1.8 on the current main HEAD
#   scripts/tag-release.sh v0.1.8            # leading 'v' allowed
#   scripts/tag-release.sh --dry-run 0.1.8   # validate + print actions, no changes
#
# Verifies HEAD's manifests advertise the version (so the tag can't land on the
# wrong commit), creates an annotated tag, and prints the push. Pushing the tag
# triggers .semaphore/release.yml (check-versions → goreleaser publish).
#
# Does NOT push — the tag push is the deliberate, irreversible publish step.

set -euo pipefail

dry_run=0
if [[ "${1:-}" == "--dry-run" ]]; then
  dry_run=1
  shift
fi

if [[ $# -lt 1 || -z "${1:-}" ]]; then
  echo "ERROR: version required" >&2
  echo "Usage: scripts/tag-release.sh [--dry-run] <X.Y.Z>" >&2
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

branch="$(git rev-parse --abbrev-ref HEAD)"
if [[ "$branch" != "main" ]]; then
  echo "ERROR: tag the release on main (currently on $branch)" >&2
  echo "  git checkout main && git pull" >&2
  exit 1
fi

if [[ -n "$(git tag -l "v$version")" ]]; then
  echo "ERROR: tag v$version already exists locally" >&2
  exit 1
fi

# HEAD must actually carry this version, or the tag would publish the wrong commit.
if ! scripts/check-manifest-versions.sh "$version"; then
  echo "ERROR: HEAD manifests do not match v$version — is the bump PR merged + pulled?" >&2
  exit 1
fi

run git tag -a "v$version" -m "v$version"

echo
if [[ "$dry_run" -eq 1 ]]; then
  echo "DRY-RUN: no tag created."
  echo "Re-run without --dry-run to apply."
else
  echo "Tagged v$version on $(git rev-parse --short HEAD). To publish:"
  echo "  git push origin v$version"
fi
