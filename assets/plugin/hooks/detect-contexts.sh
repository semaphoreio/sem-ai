#!/usr/bin/env bash
# SessionStart hook: tell the agent to pin the context it starts with instead of
# relying on the shared active-context key in ~/.sem.yaml.
#
# active-context is mutable shared state: `context switch`, `connect`, and
# `signin` all write it, so another session on the same machine can move it
# mid-run and a command that relied on it silently talks to a different
# organization. An agent cannot infer which org a repo belongs to, but "whatever
# was active at session start, keep using that" is always right — so this names
# the active context and how to pin it.
#
# Emitted for a single context too: `connect`/`signin` in another session can add
# a second one and make it active, so being alone in the file at session start is
# not a guarantee that survives the session.
#
# Deliberately separate from detect-semaphore.sh: this applies to any repo, not
# only ones with .semaphore/, and it reads a local file rather than calling the
# API, so it must not sit behind that hook's network watchdog.
#
# Best-effort and silent: emits nothing without a config, without jq, or when the
# session is already pinned.
set -uo pipefail

# Bracket ranges follow locale collation, so [A-Za-z0-9._-] admits accented and
# full-width characters under a UTF-8 locale. The name below goes into text an
# LLM reads; pin the collation so the filter means the ASCII it looks like.
export LC_ALL=C

command -v sem-ai >/dev/null 2>&1 || exit 0
command -v jq >/dev/null 2>&1 || exit 0

# Already pinned for this session — the advice below is exactly what SEM_CONTEXT
# does, and repeating it against the file's active context would contradict it.
[ -z "${SEM_CONTEXT:-}" ] || exit 0

# Reads the local config; no API call. (It can create an empty ~/.sem.yaml if
# none exists — sem-ai opens the path O_CREATE — which is why the count below
# has to tolerate zero.)
contexts="$(sem-ai context list --format json 2>/dev/null || true)"
[ -n "$contexts" ] || exit 0

count="$(printf '%s' "$contexts" | jq -r 'if type == "array" then length else 0 end' 2>/dev/null || echo 0)"
[ "$count" -ge 1 ] 2>/dev/null || exit 0

active="$(printf '%s' "$contexts" | jq -r 'map(select(.active)) | .[0].name // empty' 2>/dev/null || true)"
[ -n "$active" ] || exit 0

# A context name is a host with dots replaced by underscores, so it is bounded by
# the DNS name limit rather than anything shorter. Beyond that, or outside the
# characters a name can hold, this is not a name worth repeating into an agent's
# context — and dropping the note costs only the guidance.
case "$active" in
  *[!A-Za-z0-9._-]*) exit 0 ;;
esac
[ "${#active}" -le 253 ] || exit 0

pin="Pin this session instead of reading that key: pass \`--context $active\` on sem-ai calls, or \`export SEM_CONTEXT=$active\` once (an inline \`SEM_CONTEXT=... sem-ai ...\` only covers that one process). Over MCP, pass \`context\` on each tool call. Do not run \`context switch\` to set up for a later command — it mutates the shared key for every session on this machine, and an explicit pin makes it unnecessary. Use a different context name when the user asks for another organization. \`connect\`, \`signin\`, \`context switch\`, and \`context list\` ignore the pin, so onboarding an organization still works while pinned; the context \`connect\` creates is named after its host, so pin that name afterwards."

if [ "$count" -eq 1 ]; then
  state="sem-ai has one context in ~/.sem.yaml, \"$active\", and commands resolve it through the shared active-context key. Another session running \`connect\` or \`signin\` adds a second context and makes it active, which would move this session's organization mid-run."
else
  state="sem-ai has $count contexts in ~/.sem.yaml and the active one is \"$active\". active-context is shared mutable state — \`context switch\`, \`connect\`, and \`signin\` all write it, so another session or agent on this machine can move it mid-run and a later command silently hits the wrong organization."
fi

# Older binaries have no --context; give them the advice that works there instead
# of a flag they will reject.
if sem-ai --help 2>/dev/null | grep -q -- '--context'; then
  ctx="$state $pin"
else
  ctx="$state This sem-ai is too old for the \`--context\` pin. Chaining the switch and the command in one shell call (\`sem-ai context switch NAME && sem-ai ...\`) narrows the window — it does not close it, since those are still two processes reading the file at different moments — so prefer upgrading over relying on it."
fi

printf '%s' "$ctx" | jq -Rs '{hookSpecificOutput:{hookEventName:"SessionStart",additionalContext:.}}'
