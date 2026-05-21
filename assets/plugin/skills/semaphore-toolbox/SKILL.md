---
name: semaphore-toolbox
description: The Semaphore toolbox — CLIs preinstalled in every job for caching, artifacts, language version switching, services, retries, and SSH debugging. Authoritative reference for the cache / artifact / retry / sem-version / sem-service / checkout commands. Use when an agent needs to reason about Semaphore toolbox commands, questions arise about cache CLI semantics, artifact push/pull, sem-version, sem-service, retry, or checkout, pipeline-writing decisions involve any of these CLIs, or debugging "command not found" / "command line argument" errors involving toolbox commands.
---

# Semaphore toolbox

Every Semaphore agent ships with the [toolbox](https://github.com/semaphoreci/toolbox) preinstalled and the script `~/.toolbox/toolbox` sourced into every job's shell. That's why `checkout`, `cache`, `sem-version`, etc. are on `PATH` (or as bash functions) without any setup step.

Anything in this skill assumes the toolbox is sourced — true in every Semaphore CI job; not always true in a fresh testbox SSH (run `source ~/.toolbox/toolbox` if needed).

**Out of scope**: publishing test results — that has its own skill: `semaphore-test-results`. This skill only mentions `test-results` in passing.

## `cache` — key-based dependency / artifact cache

Project-scoped, key-addressed, LRU-evicted. Use for things that change *rarely* — language deps, compiled binaries, model files. Survives across workflows.

### Synopsis

```
cache store <key> <path>          # store one path under one key (key is unique)
cache restore <keys...>           # restore by key; comma-separated fallback keys
cache list                        # show what's cached
cache delete <key>                # remove a specific key
cache clear                       # nuke project cache
cache has_key <key>               # exit 0 if present, 1 if not
```

### Cardinality rule (FOOTGUN)

`cache store` takes **ONE path**. Not multiple. Two common wrong patterns:

```yaml
# WRONG — cache store sees `path1` as the key and treats `path2` as garbage or path
- cache store my-key path1 path2

# RIGHT — one cache store per path, or tar first
- cache store my-key-deps node_modules
- cache store my-key-build dist
# or
- tar czf bundle.tgz dist packages && cache store my-key bundle.tgz
```

If you have multiple things to cache, either issue separate `cache store` calls (each with its own key) or tar them into one path.

### Key conventions

- **Per-commit build outputs**: key with `$SEMAPHORE_GIT_SHA` (every commit gets a fresh entry; tiny risk of bloat for short-lived branches).
- **Dependency manifests**: key with `$(checksum <lockfile>)` — Semaphore's built-in checksum helper. Example: `node-modules-$(checksum package-lock.json)`.
- **Fallback chain**: `cache restore primary-key,fallback-1,fallback-main`. First match wins.

Common pattern:

```yaml
- cache restore node-modules-$(checksum package-lock.json),node-modules-main
- npm install
- cache store node-modules-$(checksum package-lock.json) node_modules
```

### Exit code on usage error (KNOWN ISSUE)

If `cache store` is invoked with bad arguments (wrong cardinality, missing path) the job may still report PASSED if the bad line is the last command. Wrap critical caching in a way that surfaces failure:

```yaml
- set -e
- cache store key path
```

…and verify the cache exists on subsequent runs (`cache has_key`).

### When to use cache vs artifact

| Lifetime / scope | Use |
|---|---|
| Survives the workflow; shared across runs | `cache` |
| One workflow only; passed between blocks/jobs | `artifact` |
| Build output ≤30s to regenerate | Skip both; rebuild in each consumer job |

Cache is keyed; artifact is path-named. Cache LRU-evicts; artifact has explicit TTL flags. For per-commit build handoff, cache keyed by `$SEMAPHORE_GIT_SHA` is usually simpler than artifact.

### FOOTGUN — cache-hit skips package postinstall hooks

If `node_modules` is restored from cache, `npm install` says "up to date" and **skips postinstall hooks**. The hooks are where downloaders like browser-test runners fetch their actual binary (separate from the npm package), so the cache hit silently breaks the binary.

Examples in the wild:
- Cypress postinstall downloads the runner binary into `~/.cache/Cypress` → next run fails with "binary not found".
- Playwright postinstall downloads browser binaries into `~/.cache/ms-playwright` → same.
- Any package with a fetch-binary postinstall — same shape.

**Rule (preferred)**: redirect the tool's binary directory into a path that's already part of your dependency-cache scope. Each tool reads an env var for its binary location:

| Tool | Env var | Set it to |
|---|---|---|
| Cypress | `CYPRESS_CACHE_FOLDER` | a subdir of `$HOME/.npm` or your project's `node_modules/.cache/cypress` |
| Playwright | `PLAYWRIGHT_BROWSERS_PATH` | similar — inside the deps cache |
| Puppeteer | `PUPPETEER_CACHE_DIR` | similar |

Set the env var in `global_job_config.env_vars:` (so every job sees it consistently) and the binary is then naturally bundled into your existing dependency-cache restore. No extra cache key, no manual postinstall trigger.

**Fallback** when an env-var redirect isn't available: explicit postinstall trigger after `npm install`, e.g. `npx cypress install` / `npx playwright install`. Adds ~10s per run but works for any tool. Or cache the binary dir separately (its own `cache restore` / `cache store` keyed on the lockfile), which is cheap at runtime but multiplies cache keys.

---

## `artifact` — workflow / job / project artifact store

Path-named, scoped to one of: `job`, `workflow`, or `project`. Used to hand files between jobs or persist outside the workflow (reports, releases, coverage).

### Synopsis

```
artifact push <scope> <path> [--destination <dst>] [--expire-in <duration>] [--force]
artifact pull <scope> <path> [--destination <dst>] [--force]
artifact yank <scope> <path>          # delete from store
```

`<scope>` ∈ `job`, `workflow`, `project`. Most common: `workflow` (handoff between blocks).

### `--force` rule (FOOTGUN)

`artifact pull` refuses to overwrite an existing file/dir at the destination. If the consumer job runs `checkout` (which usually it does) and the artifact path overlaps something in the repo (e.g. pulling `packages/` when the repo already has a `packages/` dir), the pull fails.

Fix: always pass `--force` for paths that may exist after checkout:

```yaml
- checkout
- artifact pull workflow packages --force
```

### Push pattern

```yaml
- npm run build
- artifact push workflow dist
- artifact push workflow coverage.out --expire-in 7d
```

### When to use artifact (vs cache)

- Test reports → `artifact push workflow junit.xml` (often paired with `test-results publish`)
- Coverage outputs that humans inspect later → `artifact push workflow coverage.html` with `--expire-in`
- Release artifacts → `artifact push project release.tgz` (project-scoped survives the workflow)
- Generic between-block file handoff → ok, but if the file is small and easy to regenerate, prefer rebuilding

---

## `retry` — flaky command wrapper

Re-run a command with exponential backoff. No equivalent in GHA — preinstalled here.

### Synopsis

```
retry [--sleep N] [--times N] -- <command>
```

### Examples

```yaml
- retry --times 3 --sleep 5 -- npm install
- retry --times 5 -- curl -fsSL https://example.com/dep.tgz -o dep.tgz
```

Don't blanket-wrap every command. Use only for ops known to be flaky: network installs, third-party HTTP fetches, DNS-sensitive containers.

---

## `checkout` — git clone helper

Bash function (not a binary). Clones the current commit with shallow defaults tuned for CI speed.

### Synopsis

```
checkout [--use-cache]
```

### Where to put it

Put `checkout` in `global_job_config.prologue.commands:` so every block runs it without repeating:

```yaml
global_job_config:
  prologue:
    commands:
      - checkout
```

When only one block needs the repo, scope to that block's `task.prologue` instead.

### `--use-cache`

`checkout --use-cache` uses a project-level cache of `.git/` to avoid full clones on every job. Worth it on monorepos. Trivial savings on small repos.

---

## `sem-version` — language version switcher

Bash function. Switches the active version of a language for the current job.

### Synopsis

```
sem-version <lang> <version>
```

### Supported

- Ruby, Node, Python, Go, Java, Erlang/Elixir, Scala, PHP

### Examples

```yaml
- sem-version node 20
- sem-version ruby 3.3.4
- sem-version python 3.12
```

Pair with `cache` keyed by the lockfile to skip re-installing deps when the lang version is the only thing that changed.

### macOS quirk

On macOS agents, `sem-version` only supports `ruby` and `node`. Other languages will error with "not supported in this environment" + a docs link. For other languages on macOS, install manually in a prologue command.

---

## `sem-service` — managed service starter

Starts a managed sidecar service inside the job's environment. Direct equivalent of GHA `services:`.

### Synopsis

```
sem-service start <name> [<version>]
sem-service status <name>
sem-service stop <name>
```

### Supported services

`postgres`, `mysql`, `redis`, `rabbitmq`, `memcached`, `mongodb`, `elasticsearch`.

### Examples

```yaml
- sem-service start postgres 15
- sem-service start redis 7
```

### Default credentials

Match what GHA `services:` exposes — postgres on `localhost:5432`, user `postgres`, no password, db named after the user. Don't add custom env unless you actually need it.

### Out-of-list services

Custom container images (anything outside the supported list above) → use plain `docker run` in a prologue command instead. Not a `sem-service` use case.

---

## `test-results` — publish JUnit / test reports

See dedicated skill `semaphore-test-results` for:
- How to publish from epilogue (so failures still surface results)
- Per-framework JUnit configs (Go gotestsum, RSpec, Jest, pytest, Vitest, ExUnit)
- `gen-pipeline-report` in `after_pipeline` for the aggregated UI report

This skill only flags that `test-results` exists. Go to the dedicated one for the depth.

---

## Other toolbox CLIs (brief reference)

- **`sem-context`** — read/write workflow-scoped key-value state across blocks/jobs.
- **`sem-dockerize`** — wrap a job's commands to run inside a chosen Docker image.
- **`sem-install`** — install a language version on demand (when `sem-version` says it's not available).
- **`sem-semantic-release`** — wrapper around `semantic-release` configured for Semaphore env.
- **`ssh-session-cli`** — used by the "SSH into agent" debug feature; rarely invoked directly.
- **`system-metrics-collector`** — emits CPU/RAM metrics to artifact for post-mortem.

Each is documented in `~/.toolbox/` on the agent — `head` the file for usage.

---

## Discipline

- **Source the toolbox in shells that aren't CI jobs**: `source ~/.toolbox/toolbox`. CI jobs already have it sourced.
- **Don't reinstall preinstalled tools** — verify via `probe-agent-environment` skill before adding `apt-get install` lines.
- **`set -e` at the top of multi-command blocks** so toolbox CLI usage errors fail the job instead of slipping through (see cache exit code footgun).
- **Cache keys must be deterministic** — using `$RANDOM` or timestamps as part of the key defeats the cache. Prefer `$(checksum <lockfile>)` or `$SEMAPHORE_GIT_SHA`.

## Related skills

- `semaphore-test-results` — depth on the `test-results` CLI
- `semaphore-blocks` — where toolbox commands run (block/task/job structure, parallelism)
- `semaphore-promotions` — `cache` and `artifact` in deploy pipelines
- `probe-agent-environment` — verify what's preinstalled before assuming
- `gha-to-semaphore` — when translating from `actions/cache`, `actions/upload-artifact`, etc.
