#!/usr/bin/env bash
# SessionStart hook: when ~/.sem.yaml holds more than one context, tell the agent
# to pin the one it starts with instead of relying on the shared active-context key.
#
# active-context is mutable shared state: `context switch`, `connect`, and `signin`
# all write it, so another session on the same machine can move it mid-run and a
# command that relied on it silently talks to the wrong org. An agent cannot infer
# which org a repo belongs to, but "whatever was active at session start, keep using
# that" is always right — so this names the active context and how to pin it.
#
# Deliberately separate from detect-semaphore.sh: this applies to any repo, not only
# ones with .semaphore/, and it reads a local file rather than calling the API, so it
# must not sit behind that hook's network watchdog.
#
# Best-effort and silent: emits nothing with one context, no config, or no jq.
set -uo pipefail

command -v sem-ai >/dev/null 2>&1 || exit 0
command -v jq >/dev/null 2>&1 || exit 0

# Local config read, no network.
contexts="$(sem-ai context list --format json 2>/dev/null || true)"
[ -n "$contexts" ] || exit 0

count="$(printf '%s' "$contexts" | jq -r 'if type == "array" then length else 0 end' 2>/dev/null || echo 0)"
[ "$count" -gt 1 ] 2>/dev/null || exit 0

active="$(printf '%s' "$contexts" | jq -r 'map(select(.active)) | .[0].name // empty' 2>/dev/null || true)"
[ -n "$active" ] || exit 0

# Older binaries have no --context; give them the advice that works there instead of
# a flag they will reject.
if sem-ai --help 2>/dev/null | grep -q -- '--context'; then
  ctx="sem-ai has $count contexts in ~/.sem.yaml and the active one is \"$active\". active-context is shared mutable state — \`context switch\`, \`connect\`, and \`signin\` all write it, so another session or agent on this machine can move it mid-run and a later command silently hits the wrong organization. Pin this session instead of reading that key: pass \`--context $active\` on sem-ai calls, or export \`SEM_CONTEXT=$active\` once. Over MCP, pass \`context\` on each tool call. Do not run \`context switch\` to set up for a later command — it mutates the shared key for every session on this machine, and an explicit pin makes it unnecessary. Use a different context name when the user asks for another organization."
else
  ctx="sem-ai has $count contexts in ~/.sem.yaml and the active one is \"$active\". active-context is shared mutable state — another session's \`context switch\` can move it mid-run, and this sem-ai is too old for the \`--context\` pin. When a command depends on a specific organization, chain the switch and the command in one shell call (\`sem-ai context switch NAME && sem-ai ...\`) so nothing can move the key in between."
fi

printf '%s' "$ctx" | jq -Rs '{hookSpecificOutput:{hookEventName:"SessionStart",additionalContext:.}}'
