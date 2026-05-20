#!/usr/bin/env bash
# Check that plugin manifest versions are consistent.
#
# Usage:
#   scripts/check-manifest-versions.sh                  # both files match each other
#   scripts/check-manifest-versions.sh v0.1.8           # both match the given tag
#   scripts/check-manifest-versions.sh 0.1.8            # leading 'v' optional
#
# Exits non-zero on any mismatch with a clear diagnostic.

set -euo pipefail

MARKETPLACE=".claude-plugin/marketplace.json"
PLUGIN="assets/plugin/plugin.json"

if [[ ! -f "$MARKETPLACE" || ! -f "$PLUGIN" ]]; then
  echo "ERROR: run from repo root — missing $MARKETPLACE or $PLUGIN" >&2
  exit 2
fi

if ! command -v yq >/dev/null 2>&1; then
  echo "ERROR: yq required" >&2
  exit 2
fi

market_ver=$(yq -p json -o tsv '.plugins[0].version' "$MARKETPLACE")
plugin_ver=$(yq -p json -o tsv '.version' "$PLUGIN")

if [[ "$market_ver" != "$plugin_ver" ]]; then
  echo "FAIL: plugin manifest versions disagree" >&2
  echo "  $MARKETPLACE  → $market_ver" >&2
  echo "  $PLUGIN       → $plugin_ver" >&2
  echo "  bump both with: make release VERSION=<X.Y.Z>" >&2
  exit 1
fi

if [[ $# -ge 1 ]]; then
  expected="${1#v}"
  if [[ "$market_ver" != "$expected" ]]; then
    echo "FAIL: manifest version does not match expected tag" >&2
    echo "  manifests advertise: $market_ver" >&2
    echo "  expected (from tag): $expected" >&2
    echo "  bump both with: make release VERSION=$expected" >&2
    exit 1
  fi
  echo "OK: manifests at $market_ver (matches tag v$expected)"
else
  echo "OK: manifests consistent at $market_ver"
fi
