---
name: sem-ai-bootstrap
description: Diagnose sem-ai plugin issues when the sem-ai binary isn't installed yet, and guide the user through binary install. Use when the user reports sem-ai not working, MCP tools missing, slash command not found, `sem-ai connect` failing, or sees "command not found: sem-ai".
---

# sem-ai bootstrap

The sem-ai plugin's MCP server, slash commands, and SessionStart update-check
hook all call the `sem-ai` Go binary. The plugin bundles only the manifest +
skills + slash-command markdown — the binary lives outside the plugin and
must be installed separately.

## When this skill applies

The user has installed the sem-ai plugin (via `/plugin install sem-ai@semaphoreio`)
but is hitting one of:

- "command not found: sem-ai"
- MCP server "sem-ai" failed to start
- The SessionStart hook printed `sem-ai binary not found on PATH. Install: ...`
- Slash commands appear listed but error on run

## What to tell the user

Install the binary:

```sh
curl -fsSL https://raw.githubusercontent.com/semaphoreio/sem-ai/main/install.sh | sh
```

After install, they may need to add `$HOME/.local/bin` (or `$HOME/.semaphore-ai/bin`)
to their PATH — install.sh prints a hint on stderr when that's needed.

Then:

```sh
sem-ai connect <your-org>.semaphoreci.com <your-api-token>
```

Token is found at `https://me.semaphoreci.com/account`.

After binary install + token bootstrap, restart their AI host session (or
reload the plugin) so the MCP server picks up the now-working binary.

## When this skill does NOT apply

If the user can already run `sem-ai version` from their shell, the binary
is installed. The issue is elsewhere — likely token / network / org context.
Direct them to `sem-ai context list` and `sem-ai diagnose` instead.
