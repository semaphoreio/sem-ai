---
name: probe-agent-environment
description: Spin up a short-lived Semaphore testbox to check what's actually installed on an agent — tools, versions, toolbox functions, runtimes — when the official image docs don't answer the question. Use when an agent needs to confirm whether a tool/runtime/version is preinstalled on a Semaphore agent ("does the runner have X", "what version of Y ships on ubuntu2204/ubuntu2404", "is jq/yq/gh/aws on PATH", "what toolbox functions exist"), or before adding a CI step that installs a tool — verify it isn't already preinstalled.
---

# Probe a Semaphore agent with a testbox

## When to use this

Before guessing whether something is preinstalled — and before adding an `apt-get install …` or `curl … | sh` line to a pipeline — verify. A Semaphore testbox is a short-lived agent of the chosen `machine` + `os_image` that you can run arbitrary commands against. It's the same image CI uses, so what you observe is what the pipeline will see.

Use this skill when:
- You're about to translate a GHA workflow (see `gha-to-semaphore`) and need to know if a tool is preinstalled or needs an install step.
- The official image docs ([docs.semaphore.io/reference/os-ubuntu-images](https://docs.semaphore.io/reference/os-ubuntu-images)) don't cover the specific tool/version.
- You suspect something is preinstalled as a toolbox bash function rather than a binary on `$PATH`.
- A pipeline step fails with "command not found" and you need to know whether to install or just source the toolbox.

Skip this skill when the docs already answer the question — the image manifests at the URL above are authoritative for explicitly listed tools.

## Prerequisite — a project must exist

`sem-ai testbox warmup` is scoped to a project. You need either:
- A project already on the active org → use that project's name.
- A throwaway "probe" project — create one with `sem-ai project create --skip-yaml` from inside any git repo in your namespace.

If no project exists in the active context and you can't create one, fall back to the official docs and flag the unknown.

## The three commands

```
# 1. Start a short-lived agent. ALWAYS pass --duration 5m for probes —
#    if you forget to stop, it dies fast.
sem-ai testbox warmup --project <name> --os-image ubuntu2204 --duration 5m
#   → returns testbox_id

# 2. Run probes (non-interactive stdin).
sem-ai testbox run --id <testbox_id> "<command>"

# 3. Stop it as soon as you're done — don't burn agent minutes.
sem-ai testbox stop --id <testbox_id>
```

Save the `testbox_id` from step 1; you'll need it for `run` and `stop`. If the session ends without `stop`, the testbox auto-terminates at `--duration`.

**Why such a short duration**: agent minutes are billable, and a forgotten testbox at the default 30m wastes ~25 minutes per slip. 5m gives plenty of headroom for a probe batch (which takes seconds) and limits the blast radius if you forget to clean up. Bump only if a single probe needs serial commands that genuinely take longer than ~3m.

## Common probes

### Single tool

```
sem-ai testbox run --id $TB "which yq && yq --version"
```

### Batch — many tools at once

```
sem-ai testbox run --id $TB "for cmd in yq jq gh curl docker make git aws gcloud kubectl helm; do v=\$(command -v \$cmd 2>/dev/null) && echo \"\$cmd: \$v\" || echo \"\$cmd: MISSING\"; done"
```

### Toolbox functions

The Semaphore toolbox (`~/.toolbox/toolbox`) provides bash *functions* like `checkout` and `sem-version` — they don't show up under `which` because they aren't binaries. Source the toolbox first, then check with `type`:

```
sem-ai testbox run --id $TB "source ~/.toolbox/toolbox; type checkout 2>&1 | head -3; type sem-version 2>&1 | head -3"
```

Also list everything in `~/.toolbox/`:

```
sem-ai testbox run --id $TB "ls ~/.toolbox/"
```

### Language runtimes

`node` may be a default version; `sem-version <lang> <ver>` switches it. Check both:

```
sem-ai testbox run --id $TB "node --version; ls ~/.nvm/versions/node/ 2>/dev/null; python3 --version; go version; ruby --version 2>&1 | head -1"
```

## What we already verified (ubuntu2204, as of v0.1.8)

Quick reference of facts established via testbox probes. Treat as a snapshot — re-verify if the image baseline changes.

**Preinstalled binaries**:
- `yq` v4.47.2 (`/usr/local/bin/yq`)
- `jq`, `gh`, `curl`, `gpg`, `git`, `make`
- `docker`, `docker-compose`
- `aws`, `gcloud`, `az`, `kubectl`, `helm`
- `node` (v12 and v22 available), `python3`, `go`, `java`, `mvn`, `gradle`, `yarn`

**Toolbox** (sourced via `~/.toolbox/toolbox`):
- functions: `checkout`, `sem-version`
- binaries: `cache`, `artifact`, `sem-service`, `retry`, `test-results`, `sem-context`, `sem-dockerize`, `sem-install`, `sem-semantic-release`, `ssh-session-cli`

**NOT preinstalled** (CI step must install):
- `gotestsum`, `golangci-lint`, `trivy`
- `nvm`, `rbenv`, `pyenv`, `asdf`
- `npm`, `pnpm` (oddly — `yarn` is present)
- `ruby` (only via `sem-version`)

## Discipline

- **Always stop** the testbox when done. Long-lived idle testboxes burn agent minutes.
- **Use `--duration 5m`** for probes — default is 30m, way too long if you forget to stop. Probes take seconds.
- **Match the image** to what the pipeline actually uses. If the pipeline runs on `ubuntu2404`, probe with `--os-image ubuntu2404`, not the default.
- **Match the machine** when probing tools that depend on architecture (rare; most probes are arch-agnostic).
- **Don't probe in a loop** — one batch command beats many single-tool calls. Each `testbox run` adds ~2s of sync overhead.
- **Cache the result** — once you confirm a tool's preinstall state for an image, that's stable across runs. Don't probe the same thing twice in the same session.

## Related skills

- **`gha-to-semaphore`** — main caller of this skill; uses probes to confirm preinstalls before deciding to drop or keep an install step.
- **`testbox`** — broader testbox usage (running real CI commands against local changes).
- **`semaphore-ci`** — top-level navigation.
