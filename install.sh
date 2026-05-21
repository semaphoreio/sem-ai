#!/bin/sh
set -eu

GITHUB_OWNER=semaphoreio
GITHUB_REPO=sem-ai
API_URL="https://api.github.com/repos/${GITHUB_OWNER}/${GITHUB_REPO}/releases/latest"
DL_BASE="https://github.com/${GITHUB_OWNER}/${GITHUB_REPO}/releases/download"

say() { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$*" >&2; }

TMP=$(mktemp -d 2>/dev/null || mktemp -d -t sem-ai-install)
trap 'rm -rf "$TMP"' EXIT INT TERM

# Step 1 — platform detect
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)

case "$os" in
  darwin|linux) ;;
  *)
    warn "unsupported platform: ${os}/${arch}"
    exit 1
    ;;
esac

case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *)
    warn "unsupported platform: ${os}/${arch}"
    exit 1
    ;;
esac

# Step 2 — resolve latest release tag
tag=$(curl -fsSL "$API_URL" \
  | grep '"tag_name"' \
  | sed -E 's/.*"tag_name" *: *"([^"]+)".*/\1/')

if [ -z "$tag" ]; then
  warn "failed to resolve latest release (network error or rate limit)"
  exit 1
fi

# GoReleaser strips the leading v from {{ .Version }}
ver=${tag#v}

# Step 3 — install destination detect
path_hint=0

case ":${PATH}:" in
  *":${HOME}/.local/bin:"*)
    dest_dir="${HOME}/.local/bin"
    ;;
  *)
    dest_dir="${HOME}/.semaphore-ai/bin"
    path_hint=1
    ;;
esac

dest="${dest_dir}/sem-ai"
mkdir -p "$dest_dir"

# Step 4 — skip-if-current fast-path
if [ -x "$dest" ]; then
  installed_ver=$("$dest" version 2>/dev/null \
    | grep '"version"' \
    | sed -E 's/.*"version" *: *"([^"]+)".*/\1/' \
    || true)
  installed_ver_stripped=${installed_ver#v}
  if [ -n "$installed_ver_stripped" ] && [ "$installed_ver_stripped" = "$ver" ]; then
    say "sem-ai ${tag} is already the latest version"
    say ""
    say "If you use Claude Code or Codex, refresh the plugin to pick up the latest skills:"
    say "  /plugin update sem-ai@semaphoreio"
    say "(First time? Install with:  /plugin marketplace add semaphoreio/sem-ai && /plugin install sem-ai@semaphoreio )"
    exit 0
  fi
fi

# Step 5 — download tarball + checksums.txt
asset="sem-ai_${ver}_${os}_${arch}.tar.gz"
asset_url="${DL_BASE}/${tag}/${asset}"
sums_url="${DL_BASE}/${tag}/checksums.txt"

curl -fsSL -o "${TMP}/${asset}" "$asset_url"
curl -fsSL -o "${TMP}/checksums.txt" "$sums_url"

# Step 6 — checksum verify
line=$(grep " ${asset}$" "${TMP}/checksums.txt" || true)
if [ -z "$line" ]; then
  warn "sha256 mismatch: no entry for ${asset} in checksums.txt"
  exit 1
fi

cd "$TMP"
case "$os" in
  darwin) printf '%s\n' "$line" | shasum -a 256 -c - >/dev/null 2>&1 || { warn "sha256 mismatch"; exit 1; } ;;
  linux)  printf '%s\n' "$line" | sha256sum -c -    >/dev/null 2>&1 || { warn "sha256 mismatch"; exit 1; } ;;
esac
cd - >/dev/null

# Step 7 — extract + atomic place
tar -xzf "${TMP}/${asset}" -C "$TMP"
chmod +x "${TMP}/sem-ai"
mv "${TMP}/sem-ai" "$dest"

# Step 8 — confirm
if [ "$path_hint" = "1" ]; then
  warn "note: add ${dest_dir} to your PATH (e.g. add 'export PATH=\"\$HOME/.semaphore-ai/bin:\$PATH\"' to ~/.profile)"
fi
say "installed sem-ai ${tag} to ${dest}"
say ""
say "If you use Claude Code or Codex, install (or refresh) the skill bundle:"
say "  /plugin marketplace add semaphoreio/sem-ai"
say "  /plugin install sem-ai@semaphoreio        # first time"
say "  /plugin update  sem-ai@semaphoreio        # already installed"
say ""
say "Then run /sem-ai:init in a repo to set up Semaphore CI/CD for it."
