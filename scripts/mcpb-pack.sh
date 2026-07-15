#!/usr/bin/env bash
# Pack one MCPB (.mcpb) bundle from a freshly built binary, using the official
# `mcpb pack` CLI. Invoked by GoReleaser's build post-hook once per target, so
# the bundle is produced straight from dist/ — no re-download of release assets.
#
# Usage (from repo root, as GoReleaser runs it):
#   scripts/mcpb-pack.sh <binary_path> <os> <arch> <version>
#
# One bundle per OS+arch: MCPB platform_overrides keys on OS only, not CPU arch.
# Output: dist/sem-ai_<version>_<os>_<arch>.mcpb (picked up by release.extra_files).
#
# Requires node (mcpb on PATH, else npx fetches it).

set -euo pipefail

bin_path="$1"
os="$2"
arch="$3"
ver="$4"

# GoReleaser runs this post-hook on snapshot builds too (PR CI + snapshot
# pipelines), which don't ship bundles and have no mcpb installed. Snapshots
# carry the `-next` suffix from .goreleaser.yaml's version_template — skip them.
case "$ver" in
  *-next)
    echo "skipping mcpb pack for snapshot $ver"
    exit 0
    ;;
esac

root="$(cd "$(dirname "$0")/.." && pwd)"
manifest="$root/mcpb/manifest.json"

binname="sem-ai"
[ "$os" = "windows" ] && binname="sem-ai.exe"

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/server"
cp "$manifest" "$stage/manifest.json"
cp "$bin_path" "$stage/server/$binname"
chmod +x "$stage/server/$binname"

mkdir -p "$root/dist"
out="$root/dist/sem-ai_${ver}_${os}_${arch}.mcpb"

if command -v mcpb >/dev/null 2>&1; then
  mcpb pack "$stage" "$out"
else
  npx -y @anthropic-ai/mcpb pack "$stage" "$out"
fi

echo "packed $(basename "$out")"
