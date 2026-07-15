# MCPB desktop extension

[MCPB](https://github.com/anthropics/mcpb) (MCP Bundle) packages the `sem-ai`
MCP server as a one-click desktop extension for Claude Desktop and other
MCPB-aware apps. The user installs a `.mcpb`, enters their organization host and
API token in the install dialog (token stored in the OS keychain), and the app
launches `sem-ai mcp` for them — no terminal, no `~/.sem.yaml` editing.

## How config flows

The bundle injects the user's input as environment variables:

| Manifest `user_config` | Env var               | Read by                |
| ---------------------- | --------------------- | ---------------------- |
| `host`                 | `SEMAPHORE_HOST`      | `config.Load()`        |
| `api_token`            | `SEMAPHORE_API_TOKEN` | `config.Load()`        |

Env wins over `~/.sem.yaml`, so the same env vars also configure the plain CLI
in CI or scripted contexts.

## Packaging

One bundle ships per OS+arch — MCPB `platform_overrides` keys on OS only, not
CPU architecture. Packing is folded into the GoReleaser run: a build post-hook
calls `scripts/mcpb-pack.sh` for each freshly built binary, which stages
`manifest.json` + `server/<binary>` and runs the official `mcpb pack`. The
resulting `dist/*.mcpb` files are attached to the GitHub release via
`release.extra_files` in `.goreleaser.yaml` — no separate step, no re-download
of published assets. The release job installs `mcpb` (`npm i -g
@anthropic-ai/mcpb`) before invoking GoReleaser.

`mcpb/manifest.json` is version-synced with the plugin manifests via
`scripts/release.sh` and guarded by `scripts/check-manifest-versions.sh`.

## Local dev

```sh
make mcpb                                    # build + pack into dist/ (needs node)
npx @anthropic-ai/mcpb info dist/sem-ai_dev_*.mcpb
```

Open the resulting `dist/sem-ai_dev_*.mcpb` with Claude Desktop to test the
install dialog.
