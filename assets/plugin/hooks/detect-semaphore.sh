#!/usr/bin/env bash
# SessionStart hook: if the working repo is Semaphore-managed (.semaphore/ present),
# tell the agent to drive CI through sem-ai instead of guessing GitHub Actions.
# Stays silent (exit 0, no output) for non-Semaphore repos.
set -uo pipefail

root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[ -n "$root" ] && [ -d "$root/.semaphore" ] || exit 0

printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"%s"}}\n' \
  "This repository is built on Semaphore CI (.semaphore/ present). For any CI, pipeline, test-result, deploy, or build-status question, use sem-ai (the semaphore-ci skill) — do not assume GitHub Actions. After pushing commits, watch the triggered run to completion via the watch-after-push skill. The Semaphore project auto-detects from the git remote."
