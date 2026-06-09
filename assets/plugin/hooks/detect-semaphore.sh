#!/usr/bin/env bash
# SessionStart hook: if the working repo is Semaphore-managed (.semaphore/ present),
# (1) tell the agent to drive CI through sem-ai instead of guessing GitHub Actions, and
# (2) surface the current branch's live CI state (project + workflow + result) so the
# agent starts oriented without spending a turn resolving it.
#
# Best-effort and silent: emits nothing for non-Semaphore repos, and degrades to the
# static guidance if the live status lookup is unavailable, unconfigured, or slow.
set -uo pipefail

root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[ -n "$root" ] && [ -d "$root/.semaphore" ] || exit 0

base="This repository runs its CI on Semaphore (.semaphore/ present), connected to its git host (GitHub, GitLab, or Bitbucket). For any CI, pipeline, test-result, deploy, or build-status question, prefer sem-ai (the semaphore-ci skill): 'sem-ai status' auto-detects the project + branch from git and is the is-it-green check. The git host's own checks (e.g. 'gh pr checks') mirror the same Semaphore result and are a fine fallback when sem-ai isn't connected — sem-ai is preferred because it keeps the failure drill ('sem-ai diagnose <workflow-id>') one tool away. After pushing, follow the run with the watch-after-push skill."

# Live current-branch CI state — bounded so it can never stall session start.
# `sem-ai status` auto-detects project + branch and pins the current HEAD commit.
# Timeout is plain bash (background job + watchdog) — no perl/timeout dependency,
# so the hook keeps working even where those aren't installed.
live=""
if command -v sem-ai >/dev/null 2>&1 && command -v jq >/dev/null 2>&1; then
  s=""
  tmp="$(mktemp 2>/dev/null || echo "/tmp/sem-ai-status.$$")"
  sem-ai status --format json >"$tmp" 2>/dev/null &
  sa_pid=$!
  ( sleep 8; kill "$sa_pid" 2>/dev/null ) >/dev/null 2>&1 &
  wd_pid=$!
  disown "$wd_pid" 2>/dev/null || true   # don't announce the watchdog when we kill it
  wait "$sa_pid" 2>/dev/null
  kill "$wd_pid" 2>/dev/null || true
  s="$(cat "$tmp" 2>/dev/null || true)"
  rm -f "$tmp"
  if [ -n "$s" ]; then
    live="$(printf '%s' "$s" | jq -r '
      if .multiple_projects == true then
        "Current branch maps to multiple Semaphore projects (" + (.projects | map(.project) | join(", ")) + ") — pass --project to pick one."
      elif .status == "no_workflows" then
        "No CI workflow for the current branch/commit yet — push to trigger one."
      elif .workflow_id then
        "Current CI: project=" + (.project // "?") + " branch=" + (.branch // "?")
        + " state=" + (.pipeline.state // "?")
        + (if (.pipeline.result // "") != "" then "/" + .pipeline.result else "" end)
        + " (workflow " + .workflow_id + "). Recheck with \"sem-ai status\"; drill failures with \"sem-ai diagnose " + .workflow_id + "\"."
      else "" end' 2>/dev/null || true)"
  fi
fi

ctx="$base"
[ -n "$live" ] && ctx="$base $live"

# Emit JSON with jq when available (safe escaping); fall back to a plain printf.
if command -v jq >/dev/null 2>&1; then
  printf '%s' "$ctx" | jq -Rs '{hookSpecificOutput:{hookEventName:"SessionStart",additionalContext:.}}'
else
  printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"%s"}}\n' "$base"
fi
