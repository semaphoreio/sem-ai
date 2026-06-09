---
name: init
description: Initialize Semaphore CI/CD for the current repository — bootstrap the Semaphore project, write a working `.semaphore/semaphore.yml` (translating from GitHub Actions if present, or from scratch), wire required secrets, validate, and watch the first workflow. Applies Semaphore-side defaults automatically — agent image, toolbox CLIs, `test-results` epilogue rule, sharding heuristics — by routing through the linked skills. Use when the user wants to set up CI on Semaphore, says "initialize", "bootstrap CI", "prepare CI/CD for this project", "create a workflow on Semaphore", "make a `.semaphore` config", or runs `/sem-ai:init`.
user-invocable: true
---

# `/sem-ai:init` — initialize Semaphore CI/CD for this repo

The deterministic entry point for "make CI/CD work on Semaphore for this repository". Routes to the right path based on repo state and applies the defaults documented in the linked skills — so the agent doesn't fall back to training-data defaults that miss the per-skill guidance.

This skill is an **orchestrator**. It does not duplicate depth from other skills; it loads them on demand. If you're skimming this and the answer to a question feels missing, the relevant detail is in one of the cross-linked skills below.

## When to use this vs the underlying skills directly

| Situation | Use |
|---|---|
| "Set up CI on Semaphore for this repo" (any state) | **`/sem-ai:init`** — this skill |
| "Translate my GitHub Actions to Semaphore" (explicit) | `gha-to-semaphore` (or `/sem-ai:gha-to-semaphore`) |
| "Explain how blocks / dependencies / parallelism work" | `semaphore-blocks` |
| "What's the right way to use `cache` / `artifact` / `sem-version`" | `semaphore-toolbox` |
| "Why don't my test reports show up" | `semaphore-test-results` |
| "Is X preinstalled on the agent" | `probe-agent-environment` |

`/sem-ai:init` is the user-facing slash command; the others are the depth it routes through.

## Procedure

### Step 1 — Detect repo state

```bash
ls .github/workflows/ 2>/dev/null
ls .semaphore/ 2>/dev/null
```

Branch by what exists:

| State | Path |
|---|---|
| `.github/workflows/*` present, no `.semaphore/` | **Translate path** — follow `gha-to-semaphore` end-to-end |
| Neither | **Greenfield path** — see step 2 |
| `.semaphore/*` already present | **Refine path** — ask the user before overwriting; default is to surface what's there and refine in-place rather than rewrite |

### Step 2 — Greenfield: short interview before drafting

If the repo has no existing CI, ask the user the minimal set of questions needed to draft something concrete:

1. **What languages / runtimes?** (Inspect `package.json`, `go.mod`, `Gemfile`, `pyproject.toml`, etc., propose; ask only if unclear.)
2. **What test commands?** (Read `package.json` scripts, `Makefile`, `mix.exs`, etc.)
3. **Any managed services needed?** (Postgres, Redis, MySQL — match to `sem-service` supported list; see `semaphore-toolbox`.)
4. **Any secrets used at build / test time?** (Don't ask for values — just names.)
5. **Deploy step required?** (Default: no — pipeline runs tests only; add promotion later if needed. See `semaphore-promotions`.)

Don't ask everything blindly — propose detected defaults and let the user correct.

### Step 3 — Apply Semaphore-side defaults

These come from the linked skills. Apply automatically; mention briefly in the PR body so the user knows what was chosen and why:

- **Agent**: `type: f1-standard-2`, `os_image: ubuntu2404` (from `semaphore-blocks` runner table; per the official migration guide)
- **`checkout`** in `global_job_config.prologue.commands:` (from `semaphore-toolbox`)
- **`sem-version <lang> <ver>`** for language switching, NOT `apt-get install` / `nvm use` (from `semaphore-toolbox`)
- **`cache` keyed on `$(checksum <lockfile>)` with fallback** for dependency caching (from `semaphore-toolbox` — cardinality: ONE path per `cache store`)
- **`sem-service start <name>`** for postgres/mysql/redis/etc., NOT `services:` blocks (from `semaphore-toolbox`)
- **`test-results publish` in `epilogue.always.commands:`** for JUnit reports — never inline (from `semaphore-test-results` — inline = silent on failure)
- **Keep a default reporter alongside JUnit** (from `semaphore-test-results` "Anti-pattern — junit-only reporter") — junit-only reporters hide failure detail in the log; agents end up patching pipeline to debug. Use framework defaults + junit, not junit alone.
- **For postinstall-fetched binaries** (Cypress, Playwright, Puppeteer), set the redirect env var (`CYPRESS_CACHE_FOLDER`, `PLAYWRIGHT_BROWSERS_PATH`, `PUPPETEER_CACHE_DIR`) in `global_job_config.env_vars:` to a subdir of an already-cached path (from `semaphore-toolbox` postinstall footgun) — avoids the silent cache-hit-skips-postinstall failure.
- **Explicit `dependencies:` on every block** (from `semaphore-blocks` — all-or-none rule)
- **`auto_cancel`** at pipeline root — cancel in-flight runs on non-default branches:
  ```yaml
  auto_cancel:
    running:
      when: "branch != 'main' AND branch != 'master'"
  ```
  Always apply on greenfield. For translate path, apply unless the user's GHA had explicit `concurrency:` rules that contradict (rare). (from `semaphore-blocks` `auto_cancel` section)
- **Sharding**: do NOT auto-apply. Run the cost-benefit check from `semaphore-blocks` "Is it actually a wall-clock win" first — parallelism is a win only when tests dominate setup AND setup is cached/fast. If the check passes and there are obvious shard candidates (Cypress > 20 specs, Jest > 50 files, RSpec > 50, pytest > 100 tests), flag in PR body as a suggestion with the expected wall-clock delta. Don't apply on first draft.

If unsure whether a tool is preinstalled (uncommon flag, language version, custom CLI), spawn a short-lived testbox via `probe-agent-environment` — never write `apt-get install` / `curl | bash` for something Semaphore likely already ships.

### Step 4 — Bootstrap the Semaphore project (if needed)

```bash
sem-ai project create --skip-yaml
```

`--skip-yaml` because we'll write the yaml ourselves in step 5. If the project already exists, `sem-ai project create` returns `{status: "exists", ...}` and exits 0 — safe to run.

### Step 5 — Write the pipeline file

Default file: `.semaphore/semaphore.yml`. For multi-pipeline cases (e.g. release on a separate promotion), additional files go under `.semaphore/<name>.yml`.

Apply the defaults from step 3. For translate path, follow the `gha-to-semaphore` mapping table; for greenfield, write directly using the language's standard test invocation plus the defaults.

### Step 6 — Validate

```bash
sem-ai yaml validate --file .semaphore/semaphore.yml
```

Iterate until clean. (Note: `sem-ai yaml validate` is currently authoritative for syntax; do not push if it fails for reasons other than transient infra errors.)

### Step 7 — Wire secrets

If step 2 (or the translate path) surfaced secret names, for each:

```bash
sem-ai secret create <NAME> --env <NAME>=<value>
```

**Never invent values.** Always ask the user. List every required secret in the PR body so the user can audit before merge.

### Step 8 — Open a branch + PR

- Branch name: `ci/semaphore` (or whatever fits the repo's convention)
- PR body must include:
  - **What was created** — block summary, defaults chosen
  - **What was translated / from scratch** — clearly mark which
  - **Secrets required** — names only
  - **Differences vs prior CI** (if translate path) — fail-fast dropped, runner image picked, etc.
  - **Speed suggestions** — sharding candidates flagged but not auto-applied
- Do NOT push to upstream of an OSS repo. Work on the user's fork.
- Do NOT merge the PR yourself. Leave it for the user.

### Step 9 — Watch the first workflow

Hand off to the `watch-after-push` skill: find the run for the pushed HEAD commit, then watch it to completion. `--project` auto-detects from the `origin` remote — pass it only to override or when the repo maps to multiple projects.

```bash
# Locate the run by the pushed HEAD commit, then watch it to green:
sem-ai workflow list --limit 1   # confirm the run for the new commit; pass --project only to override
sem-ai watch <wf>
```

On failure: `sem-ai job log <job-id>`. Iterate fixes; surface any patterns worth feeding back as skill improvements.

## Discipline

- **One skill per concern** — don't restate `cache` semantics or `test-results` rules here; route through the depth-skills. Keeping this skill thin = easier to maintain.
- **Ask before overwriting** — never blow away an existing `.semaphore/` without explicit user confirmation.
- **Never invent secret values** — universal rule. Ask.
- **Don't merge user PRs** — produce the artifact; the user reviews and merges.
- **Surface what defaults were applied** — agents inheriting context from this skill should know that "the pipeline uses `f1-standard-2`" wasn't arbitrary — it's the documented default. Same for `test-results` in epilogue, etc.

## Related skills

- `gha-to-semaphore` — translation procedure + bucket classification + PR body template
- `semaphore-blocks` — block/task/job model, dependencies, parallelism, sharding shard-flag table, agent selection
- `semaphore-toolbox` — `cache`, `artifact`, `sem-version`, `sem-service`, `retry`, `checkout` CLIs + footguns
- `semaphore-test-results` — JUnit publish + epilogue rule + per-framework configs + pipeline-level aggregation
- `semaphore-promotions` — deploy gates, parameters, promotion chains (when the user asks for a deploy step)
- `probe-agent-environment` — verify what's preinstalled before adding install steps
- `manage-infra` — secret create, deploy targets, agent types
