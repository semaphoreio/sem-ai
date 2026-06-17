#!/usr/bin/env bash
# Check that plugin manifest versions are consistent.
#
# Usage:
#   scripts/check-manifest-versions.sh                  # all manifests match each other
#   scripts/check-manifest-versions.sh v0.1.8           # all match the given tag
#   scripts/check-manifest-versions.sh 0.1.8            # leading 'v' optional
#
# Covers every file that carries the plugin version:
#   .claude-plugin/marketplace.json        (.plugins[0].version)
#   assets/plugin/plugin.json              (.version)
#   assets/plugin/.codex-plugin/plugin.json (.version)
#
# Exits non-zero on any mismatch with a clear diagnostic.

set -euo pipefail

MARKETPLACE=".claude-plugin/marketplace.json"
PLUGIN="assets/plugin/plugin.json"
CODEX="assets/plugin/.codex-plugin/plugin.json"

for f in "$MARKETPLACE" "$PLUGIN" "$CODEX"; do
  if [[ ! -f "$f" ]]; then
    echo "ERROR: run from repo root — missing $f" >&2
    exit 2
  fi
done

if ! command -v yq >/dev/null 2>&1; then
  echo "ERROR: yq required" >&2
  exit 2
fi

market_ver=$(yq -p json -o tsv '.plugins[0].version' "$MARKETPLACE")
plugin_ver=$(yq -p json -o tsv '.version' "$PLUGIN")
codex_ver=$(yq -p json -o tsv '.version' "$CODEX")

if [[ "$market_ver" != "$plugin_ver" || "$market_ver" != "$codex_ver" ]]; then
  echo "FAIL: plugin manifest versions disagree" >&2
  echo "  $MARKETPLACE  → $market_ver" >&2
  echo "  $PLUGIN       → $plugin_ver" >&2
  echo "  $CODEX        → $codex_ver" >&2
  echo "  bump all with: make release VERSION=<X.Y.Z>" >&2
  exit 1
fi

if [[ $# -ge 1 ]]; then
  expected="${1#v}"
  if [[ "$market_ver" != "$expected" ]]; then
    echo "FAIL: manifest version does not match expected tag" >&2
    echo "  manifests advertise: $market_ver" >&2
    echo "  expected (from tag): $expected" >&2
    echo "  bump all with: make release VERSION=$expected" >&2
    exit 1
  fi
  echo "OK: manifests at $market_ver (matches tag v$expected)"
else
  echo "OK: manifests consistent at $market_ver"
fi
