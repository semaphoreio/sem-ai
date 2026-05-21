---
name: gha-to-semaphore
description: Translate a repo's GitHub Actions workflows into an equivalent Semaphore pipeline. This skill is ONLY about the GHA→Semaphore mapping and the conversion procedure. For Semaphore-side depth (cache CLI, test-results, blocks structure, sharding, promotions) defer to the linked skills.
trigger: user asks to convert/port/migrate GitHub Actions to Semaphore, "translate this workflow", "set up Semaphore CI for this repo with the GHA workflows", "convert ci.yml", or repo has `.github/workflows/` and the user wants Semaphore instead
---

# Convert GitHub Actions to a Semaphore pipeline

**Authoritative reference**: [docs.semaphore.io/getting-started/migration/github-actions](https://docs.semaphore.io/getting-started/migration/github-actions). Mappings below extend it with constructs the migration guide doesn't cover (matrix, `if:`, reusable workflows, custom actions, `on:` triggers, sharding).

**Scope of this skill**: only the GHA→Semaphore translation friction. For Semaphore concepts referenced below, follow the cross-links — don't inline depth here.

## Why this conversion usually shrinks YAML

Semaphore agents ship with the [toolbox](https://github.com/semaphoreci/toolbox) preinstalled. Most GHA `uses: actions/...@v*` setup boilerplate exists to install tools Semaphore already has on PATH. A 200-line GHA workflow usually becomes a 60–90 line Semaphore pipeline because each `uses:` setup collapses into a single `prologue:` line.

When drafting the translation, **don't translate setup actions into equivalent `apt-get install` / `curl | bash` commands** — assume the toolbox is on PATH and use its CLIs directly. See `semaphore-toolbox` for the full surface and `probe-agent-environment` to verify what's preinstalled if uncertain.

## The three buckets

Every GHA construct lands in one of:

1. **Direct mapping** — there is a 1:1 Semaphore equivalent. Translate without asking.
2. **Adapted mapping** — Semaphore expresses the same idea with a different shape (e.g. matrix → parameterized jobs). Translate, but call out the shape change in the PR body.
3. **Manual review** — no clean equivalent (custom actions, reusable workflows, GHA-only payload contexts). Leave a `# TODO(gha-to-semaphore):` comment in the yaml and list the item in the PR body.

## High-level shape mapping

Each row points at the skill that holds the Semaphore-side depth — read those for syntax, footguns, exact CLI arguments.

| GitHub Actions | Semaphore | Bucket | Depth lives in |
|---|---|---|---|
| `name:` (workflow) | `name:` (pipeline) | direct | `semaphore-blocks` |
| `jobs:` | `blocks:` | direct | `semaphore-blocks` |
| `jobs.<id>.steps:` | `blocks[].task.jobs[].commands` | direct | `semaphore-blocks` |
| `jobs.<id>.runs-on:` | `agent.machine.{type,os_image}` (see runner mapping below) | adapted | `semaphore-blocks` |
| `jobs.<id>.needs:` | `blocks[].dependencies:` | direct | `semaphore-blocks` |
| `jobs.<id>.strategy.matrix:` | matrix block or separate blocks (see "Matrix" below) | adapted | `semaphore-blocks` |
| `jobs.<id>.if:` | `blocks[].run.when:` (see "Conditionals" below) | adapted | `semaphore-blocks` |
| `jobs.<id>.env:` | `task.env_vars:` | direct | `semaphore-blocks` |
| `jobs.<id>.services:` | `sem-service start <name>` in prologue | direct | `semaphore-toolbox` |
| `actions/checkout@v*` | `checkout` (preinstalled; usually in `global_job_config.prologue`) | direct | `semaphore-toolbox` |
| `actions/setup-{node,go,python,ruby,java,…}` | `sem-version <lang> <ver>` | direct | `semaphore-toolbox` |
| `actions/cache@v*` | `cache restore` / `cache store` (key conventions, cardinality, footguns) | adapted | `semaphore-toolbox` |
| `actions/upload-artifact@v*` / `actions/download-artifact@v*` | `artifact push <scope> <path>` / `artifact pull <scope> <path> [--force]` | direct | `semaphore-toolbox` |
| inline shell retry loop / `nick-fields/retry@v*` | `retry <cmd>` | direct | `semaphore-toolbox` |
| `EnricoMi/publish-unit-test-result-action@v*` / `dorny/test-reporter@v*` / `mikepenz/action-junit-report@v*` | `test-results publish junit.xml --name <suite>` in epilogue | adapted | `semaphore-test-results` |
| `secrets.<NAME>` reference | bind via `secrets:` block; pre-create with `sem-ai secret create` (see "Secrets" below) | adapted | `manage-infra` |
| workflow-root `env:` | `global_job_config.env_vars:` | direct | `semaphore-blocks` |
| `on: push` / `on: pull_request` | pipeline runs on push by default; PR runs configured per-project (no YAML opt-in) | adapted | — |
| `workflow_dispatch` with `inputs:` | promotion with parameters | adapted | `semaphore-promotions` |
| reusable workflow (`uses: ./.github/workflows/...`) | no equivalent — flag for manual review | manual | — |
| GHA expressions (`${{ ... }}`) beyond `ref`/`event_name` | substitute with env vars or promotion parameters; many cases need manual rewrite | adapted | — |
| `concurrency:` (cancel-in-progress, group) | partial via `auto_cancel:` at pipeline root | adapted | `semaphore-blocks` |
| `permissions:` (GITHUB_TOKEN scopes) | N/A — drop and note | manual | — |

## Runner mapping (`runs-on` → `agent.machine`)

| GHA `runs-on` | Semaphore `agent.machine` |
|---|---|
| `ubuntu-latest` / `ubuntu-24.04` | `type: f1-standard-2`, `os_image: ubuntu2404` *(official-doc default)* |
| `ubuntu-22.04` | `type: f1-standard-2`, `os_image: ubuntu2204` |
| `macos-latest` / `macos-14` | `type: a1-standard-4`, `os_image: macos-xcode15` |
| self-hosted with label `X` | self-hosted Semaphore agent type `s1-X` if one exists (`sem-ai agent types`) |

Default when unclear: `type: f1-standard-2`, `os_image: ubuntu2404`.

## Conditionals (`if:` → `run.when`)

Semaphore's `run.when` uses a different DSL. Conservative quick table:

| GHA expression | Semaphore `run.when` |
|---|---|
| `github.ref == 'refs/heads/main'` | `branch = 'main'` |
| `startsWith(github.ref, 'refs/tags/')` | `tag =~ '.*'` |
| `github.event_name == 'pull_request'` | `pull_request =~ '.*'` |
| anything using `github.event.*` payload fields | flag for manual review |

When in doubt, leave `# TODO(gha-to-semaphore): translate condition` and surface in PR body.

## Matrix (`strategy.matrix` → matrix or separate blocks)

- Single-axis matrices (e.g. `go: ['1.21', '1.22']`) → Semaphore `matrix:` on a job. Cleanest.
- Multi-axis where all axes map to the same agent → still a `matrix:` on the job (multiplies out).
- `matrix.os` with distinct OS images → split into separate blocks (one per OS) with different `agent.machine.os_image`.
- `strategy.fail-fast` and `strategy.max-parallel` have no direct equivalent — drop them; mention in PR body.

For job-level fan-out (run the *same* job N times on N agents to shard test work), use `parallelism: N` — see `semaphore-blocks` "Sharding one job across N parallel runs".

## Secrets

GHA `secrets.X` implicitly assumes the secret exists. Semaphore secrets are explicit org-level resources, then bound to a task with `secrets:`.

Procedure:
1. Grep `.github/workflows/*` for every `secrets.<NAME>`. Dedupe.
2. For each, `sem-ai secret create <NAME> --env <NAME>=<value>` — **the user supplies the value; never invent or copy from GHA**.
3. In the translated yaml, attach at the lowest scope that needs it (block-level via `task.secrets:`).
4. List every required secret in the PR body so the user verifies they exist before merge.

See `manage-infra` for `secret create` flags and scoping.

## Bucket-3 — manual review

Leave a `# TODO(gha-to-semaphore):` comment in the yaml and list each in the PR body:

- **`uses: <author>/<action>@v*`** (any non-`actions/*` custom action) — no equivalent. Rewrite as inline commands.
- **`uses: ./.github/actions/<name>`** (local composite actions) — same.
- **Reusable workflows** (`uses: ./.github/workflows/<name>.yml`) — Semaphore has promotions + pipeline files but no direct includes; case-by-case.
- **`services:` with custom images** (anything outside `sem-service`'s supported list) — translate to `docker run` in a prologue.
- **GitHub Pages / release actions** (`peaceiris/actions-gh-pages`, `softprops/action-gh-release`, etc.) — use `sem-ai deploy` + `gh release create` equivalents.
- **`environment:` (GitHub Environments)** — approximate with Semaphore deploy targets + `subject_rules` (see `manage-infra`); GHA approval gates don't translate.
- **`workflow_dispatch.inputs.<X>`** beyond simple strings — promotion parameters cover string inputs; complex `inputs:` (choice with dynamic options, secrets-in-inputs) need rewrite. See `semaphore-promotions`.
- **GHA expression contexts** using `github.event.*` payload fields, `vars.*`, `inputs.*` beyond promotion params — Semaphore env vars differ; manual.
- **`permissions:` (GITHUB_TOKEN scopes / OIDC `id-token: write`)** — N/A on Semaphore; drop. Note any downstream features that depended on it (npm provenance, GitHub releases as the workflow user, OIDC for cloud deploys).
- **`concurrency:` cancel-in-progress** — partial via `auto_cancel:` at pipeline root; flag for review.

## Procedure (8-step run sheet)

When invoked, do the following in order:

1. **Inventory**. `ls .github/workflows/` — list every workflow. Confirm with the user which to convert (default: all).
2. **Read each workflow** and classify every construct using the buckets above.
3. **Draft** `.semaphore/semaphore.yml` (and `.semaphore/<name>.yml` for additional workflows). Apply the mapping table; use the linked skills for syntax details.
4. **Speed-scan** — for any test job that looks long-running (Cypress, Playwright, big Jest/RSpec/pytest), call out `parallelism: N` + the runner's native shard flag in the PR body and (optionally) apply it. See `semaphore-blocks` "Sharding". Don't auto-apply on first draft; surface as a suggestion.
5. **Validate** with `sem-ai yaml validate --file .semaphore/semaphore.yml`. Iterate until clean.
6. **Bootstrap project** if missing: `sem-ai project create --skip-yaml` (yaml already exists from step 3).
7. **Secrets**: list every required secret to the user; for each, run `sem-ai secret create` only after the user supplies the value.
8. **Open PR** on the user's fork with:
   - The new `.semaphore/*.yml` files
   - A PR body that includes:
     - "What was converted" — direct + adapted mappings, by block
     - "What needs your review" — bucket-3 items with line refs
     - "Secrets required" — list created via `sem-ai secret create`
     - "Differences in behavior" — fail-fast dropped, runner image picked, PR-trigger config moved server-side, etc.
     - "Speed suggestions" — sharding candidates flagged in step 4
9. **First run**: after merge, watch with `sem-ai workflow_list --project <name> --limit 1` and `sem-ai job_log` on any failure. Iterate.

## PR body template

```markdown
## Summary
Converts `<n>` GitHub Actions workflow(s) to Semaphore pipeline(s).
Original workflows untouched — both CI systems can run side-by-side.

## What was converted
- `<gha-workflow-1>.yml` → `.semaphore/semaphore.yml`
  - <block name>: <jobs and what they do>
  - …

## What needs your review
- <bucket-3 item, file:line, reason>
- …

## Secrets required (already created)
- `<SECRET_NAME>` — used by <block>

## Differences in behavior
- Runner: GHA `ubuntu-latest` → Semaphore `f1-standard-2 / ubuntu2404`
- `strategy.fail-fast` dropped (no equivalent)
- PR-trigger config moved to Semaphore project settings
- …

## Speed suggestions
- Cypress job has 58 specs — consider `parallelism: 4` + cypress-split plugin
- …
```

## Output discipline

- **Do not modify `.github/workflows/*`** unless the user explicitly asks to deprecate GHA in the same PR.
- **Do not invent secret values.** Always ask.
- **Do not push to upstream** of an OSS repo. Fork to the user's namespace first; demo on the fork.
- **Do not merge** the conversion PR yourself — leave it open as the visible artifact for review.
- **Bucket-3 = clear comment**, not silent drop. `# TODO(gha-to-semaphore):` is the marker.

## Out of scope (v1)

- Migrating GitHub Actions Marketplace plugins beyond the `actions/*` core set
- Multi-platform matrices that span Linux + macOS + Windows (Semaphore doesn't run Windows agents in most orgs)
- GitOps-flavored deploy workflows that depend on GHA OIDC (`id-token: write`)
- Auto-removing `.github/workflows/*` after migration — leave that to the user

## Related skills

- **`semaphore-toolbox`** — `checkout`, `cache`, `artifact`, `sem-version`, `sem-service`, `retry` CLIs + footguns
- **`semaphore-test-results`** — `test-results publish` (epilogue rule, per-framework JUnit configs, pipeline aggregation)
- **`semaphore-blocks`** — blocks / tasks / jobs structure, dependencies, parallelism + sharding, status aggregation
- **`semaphore-promotions`** — deploy gates, promotion parameters, `workflow_dispatch` translation
- **`manage-infra`** — `sem-ai secret create`, deploy targets, agent types
- **`probe-agent-environment`** — verify what's preinstalled on the agent before adding install steps
- **`sem-ai project create`** — bootstrap before first push
