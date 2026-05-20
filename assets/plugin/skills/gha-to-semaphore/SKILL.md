---
name: gha-to-semaphore
description: Translate a repo's GitHub Actions workflows into an equivalent Semaphore pipeline. Read .github/workflows/*.yml, emit .semaphore/semaphore.yml, map secrets, flag the constructs that need manual review.
trigger: user asks to convert/port/migrate GitHub Actions to Semaphore, "translate this workflow", "set up Semaphore CI for this repo", "convert ci.yml", or has a .github/workflows/ directory and asks to use Semaphore instead
---

# Convert GitHub Actions to a Semaphore pipeline

**Authoritative reference**: [docs.semaphore.io/getting-started/migration/github-actions](https://docs.semaphore.io/getting-started/migration/github-actions). The mappings in that doc are the source of truth; mappings below extend it with constructs the official doc doesn't cover (matrix, `if:`, reusable workflows, custom actions, `on:` triggers).

## Why this conversion shrinks YAML

Semaphore agents ship with a [toolbox](https://github.com/semaphoreci/toolbox) preinstalled on every machine. Most GHA `uses: actions/...@v*` boilerplate exists to bootstrap tools that Semaphore already has on PATH:

| GHA action (must be installed) | Semaphore (preinstalled) |
|---|---|
| `actions/checkout` | `checkout` |
| `actions/cache` | `cache restore` / `cache store` |
| `actions/upload-artifact` / `actions/download-artifact` | `artifact push` / `artifact pull` |
| `actions/setup-{go,node,python,ruby,…}` | `sem-version <lang> <ver>` |
| service containers (`services:`) | `sem-service start <postgres\|mysql\|redis\|rabbitmq\|memcached\|mongodb\|elasticsearch>` |
| (nothing) — manual retry loop in shell | `retry <cmd>` (re-run flaky commands with backoff) |
| (nothing) — needs `tmate` action | built-in SSH-into-agent debugging |
| test-result aggregation requires a Marketplace action | `test-results publish junit.xml` |

The practical effect: a 200-line GHA workflow usually becomes a 60-90 line Semaphore pipeline, because the `uses:` setup steps collapse into a single `prologue:` line each.

When you're drafting the translation, **don't** translate setup actions into equivalent `apt-get install` / `curl | bash` commands — assume the toolbox is on PATH and use its CLIs directly.

## What this skill does

Given a repository that uses (or used) GitHub Actions, produce an equivalent `.semaphore/semaphore.yml` and the supporting `sem-ai` commands to make the pipeline runnable on Semaphore. The skill is rule-driven — every GHA construct lands in one of three buckets:

1. **Direct mapping** — there is a 1:1 Semaphore equivalent. Translate without asking.
2. **Adapted mapping** — Semaphore expresses the same idea with a different shape (e.g. matrix → parameterized jobs). Translate, but call out the shape change in the PR body.
3. **Manual review** — no clean equivalent (custom actions, reusable workflows, GHA-only services). Leave a `# TODO(gha-to-semaphore):` comment in the yaml and list the item in the PR body.

## High-level shape mapping

| GitHub Actions | Semaphore | Bucket |
|---|---|---|
| `name:` (workflow) | `name:` (pipeline) | direct |
| `jobs:` | `blocks:` | direct |
| `jobs.<id>.steps:` | `blocks[].task.jobs[].commands` | direct |
| `jobs.<id>.runs-on:` | `agent.machine.type` + `os_image` (see runner mapping) | adapted |
| `jobs.<id>.needs:` | `blocks[].dependencies:` | direct |
| `jobs.<id>.strategy.matrix:` | matrix block: `blocks[].task.jobs[].matrix:` (or one job per combo) | adapted |
| `jobs.<id>.if:` | `blocks[].run.when:` | adapted (DSL differs, see "Conditionals") |
| `jobs.<id>.env:` | `blocks[].task.env_vars:` | direct |
| `jobs.<id>.services:` (postgres, redis, mysql, …) | `sem-service start <name>` in `prologue` or first command of the block | direct |
| `actions/checkout@v*` | the built-in `checkout` command (prefer `global_job_config.prologue.commands: [checkout]` to share across blocks) | direct |
| `actions/setup-go@v*` | `sem-version go <ver>` | direct |
| `actions/setup-node@v*` | `sem-version node <ver>` | direct |
| `actions/setup-python@v*` | `sem-version python <ver>` | direct |
| `ruby/setup-ruby@v*` | `sem-version ruby <ver>` | direct |
| `actions/cache@v*` | `cache restore` / `cache store` | adapted |
| `actions/upload-artifact@v*` | `artifact push workflow <path>` | direct |
| `actions/download-artifact@v*` | `artifact pull workflow <path>` | direct |
| inline shell retry loop / `nick-fields/retry@v*` | `retry <cmd>` (preinstalled, exponential backoff) | direct |
| `EnricoMi/publish-unit-test-result-action@v*` / `dorny/test-reporter@v*` | `test-results publish junit.xml --name <suite>` | adapted |
| `secrets.<NAME>` reference | bind via `secrets:` block; pre-create with `sem-ai secret create` | adapted |
| `env:` at workflow root | `global_job_config.env_vars:` | direct |
| `on: push` / `on: pull_request` | pipeline auto-runs on push by default; PR runs configured per-project (no YAML opt-in) | adapted |
| reusable workflow (`uses:`) | no equivalent — flag for manual review | manual |
| GHA expressions (`${{ ... }}`) | substitute with env vars or `parameters` (only inside parametrized blocks/promotions) | adapted |

## Runner mapping (runs-on)

| GHA `runs-on` | Semaphore `agent.machine` |
|---|---|
| `ubuntu-latest` / `ubuntu-24.04` | `type: f1-standard-2`, `os_image: ubuntu2404` *(official-doc default)* |
| `ubuntu-22.04` | `type: f1-standard-2`, `os_image: ubuntu2204` |
| `macos-latest` / `macos-14` | `type: a1-standard-4`, `os_image: macos-xcode15` (adjust to org's available types) |
| self-hosted with label `X` | self-hosted Semaphore agent type `s1-X` if one exists — list available types with `sem-ai agent types` |

Default if unclear: `type: f1-standard-2`, `os_image: ubuntu2404` (matches the migration guide).

## Checkout pattern

The official migration guide recommends putting `checkout` in `global_job_config.prologue` so every block runs it without repeating:

```yaml
global_job_config:
  prologue:
    commands:
      - checkout
```

Use this whenever the workflow checks out the repo in every job (which is the common case). When only one job needs the repo, drop `checkout` into that block's `task.prologue` instead.

## Services (`services:` → `sem-service`)

GHA:

```yaml
services:
  postgres:
    image: postgres:15
  redis:
    image: redis:7
```

Semaphore:

```yaml
blocks:
  - name: Test
    task:
      prologue:
        commands:
          - sem-service start postgres 15
          - sem-service start redis 7
      jobs:
        - name: ...
```

Notes:
- `sem-service` supports the common managed services (postgres, mysql, redis, rabbitmq, memcached, mongodb, elasticsearch). Custom container images outside that list → flag for review.
- The default credentials and ports match what GHA's `services:` exposes (e.g. postgres on `localhost:5432`, user `postgres`, no password). Don't add custom `env:` unless the GHA config did.

## Conditionals (jobs.<id>.if)

Semaphore's `run.when` uses a different DSL — it has `branch`, `tag`, `pull_request`, `change_in('path')`, etc. Conservative translation table:

| GHA expression | Semaphore `run.when` |
|---|---|
| `github.ref == 'refs/heads/main'` | `branch = 'main'` |
| `startsWith(github.ref, 'refs/tags/')` | `tag =~ '.*'` |
| `github.event_name == 'pull_request'` | `pull_request =~ '.*'` |
| anything that uses `github.event.*` payload fields | flag for manual review |

When in doubt, leave a `# TODO(gha-to-semaphore): translate condition` comment and surface in PR body.

## Matrix → matrix

GHA:

```yaml
strategy:
  matrix:
    go: ['1.21', '1.22']
    os: [ubuntu-22.04]
```

Semaphore:

```yaml
- name: Test
  task:
    jobs:
      - name: Test
        matrix:
          - env_var: GO_VERSION
            values: ['1.21', '1.22']
        commands:
          - sem-version go $GO_VERSION
          - go test ./...
```

Notes:
- `matrix.os` only translates cleanly if the OS set maps to one runner image — otherwise split into separate blocks (one per OS).
- `strategy.fail-fast` and `strategy.max-parallel` have no direct Semaphore equivalent — drop them; document in PR body.

## Secrets

GHA `secrets.X` references implicitly assume the secret exists in the repo/org. On Semaphore, secrets are explicit — they're org-level resources, created out-of-band, then bound to a task with `secrets:`.

Procedure:

1. Grep the GHA yaml for every `secrets.<NAME>` reference. Dedupe.
2. For each, run:
   ```
   sem-ai secret create <NAME> --env <NAME>=<value>
   ```
   The user supplies the value — never invent or copy values from GHA.
3. In the translated pipeline, attach the secret at the lowest scope that needs it:
   ```yaml
   blocks:
     - name: Deploy
       task:
         secrets:
           - name: <NAME>
         jobs:
           - name: ...
   ```
4. List every secret the conversion expects in the PR body so the user can verify they exist on Semaphore before merge.

## Caching

GHA:

```yaml
- uses: actions/cache@v4
  with:
    path: ~/.cache/go-build
    key: ${{ runner.os }}-go-${{ hashFiles('go.sum') }}
    restore-keys: ${{ runner.os }}-go-
```

Semaphore:

```yaml
commands:
  - cache restore go-mod-$(checksum go.sum),go-mod-main
  - go build ./...
  - cache store go-mod-$(checksum go.sum) ~/.cache/go-build
```

Notes:
- Semaphore's `cache` CLI takes positional names and optional fallback keys (comma-separated). Closest mapping: primary key = exact match, restore-keys = fallback list.
- `hashFiles('go.sum')` → `$(checksum go.sum)`.
- `runner.os` → drop; Semaphore caches are per-project, not per-OS, unless you intentionally key by image.

## Constructs that need manual review

Leave a `# TODO(gha-to-semaphore):` comment in the yaml and list each in the PR body:

- **`uses: <author>/<action>@v*`** (any non-`actions/*` custom action) — no equivalent. The user must rewrite as inline commands or pick a Semaphore-native alternative.
- **`uses: ./.github/actions/<name>`** (local composite actions) — same as above.
- **Reusable workflows** (`uses: ./.github/workflows/<name>.yml`) — Semaphore has promotions + pipeline files but not direct includes; needs case-by-case design.
- **`services:` with custom images** (not in the managed `sem-service` list — postgres/mysql/redis/rabbitmq/memcached/mongodb/elasticsearch) — translate to `docker run` in a prologue; flag for review.
- **GitHub Pages / release actions** (`peaceiris/actions-gh-pages`, `softprops/action-gh-release`, etc.) — use `sem-ai deploy` + `gh release create` equivalents; flag.
- **GHA expression contexts** that use `github.*` payload fields beyond `ref`/`event_name` — Semaphore env vars are different; flag.
- **`concurrency:`** (cancel-in-progress, group) — partial mapping via `auto_cancel:` in pipeline yaml; flag for review.
- **`permissions:`** (GITHUB_TOKEN scope) — N/A on Semaphore; drop and note.

## Procedure (the actual run sheet)

When invoked, do the following in order:

1. **Inventory**. `ls .github/workflows/` — list every workflow. Confirm with the user which to convert (default: all).
2. **Read each workflow** and classify every construct into the buckets above.
3. **Draft** `.semaphore/semaphore.yml` using the mappings. If there are multiple workflows, default to one pipeline file per workflow under `.semaphore/<name>.yml`, with `semaphore.yml` as the primary.
4. **Validate** with `sem-ai yaml_validate .semaphore/semaphore.yml`. Iterate until clean.
5. **Bootstrap project** (only if not already a Semaphore project): `sem-ai project create --skip-yaml` (yaml already exists from step 3).
6. **Secrets**: list every required secret to the user; for each, run `sem-ai secret create` only after the user supplies the value.
7. **Open PR** on the repo with:
   - The new `.semaphore/*.yml` files
   - A PR body that includes:
     - "What was converted" — direct + adapted mappings, by block
     - "What needs your review" — bucket-3 items with line refs
     - "Secrets required" — list created via `sem-ai secret create`
     - "Differences in behavior" — fail-fast dropped, runner image picked, PR-trigger config moved server-side, etc.
8. **First run**: after merge, watch with `sem-ai workflow_list --project <name> --limit 1` and `sem-ai job_log` on any failure. Iterate.

## Output discipline

- Do not modify `.github/workflows/*` unless the user explicitly asks to deprecate GHA in the same PR.
- Do not invent secret values. Always ask.
- Do not push to upstream unless the user confirms — when converting an OSS repo for demo purposes, work on the user's fork.
- When a construct lands in bucket 3, prefer a clear `# TODO(gha-to-semaphore):` comment over silently dropping it.

## Related skills + tools

- **`semaphore-blocks`** — exact semantics of blocks/tasks/jobs (read this first if you're unsure how to structure a translation)
- **`semaphore-promotions`** — for translating deploy-flavored workflows
- **`semaphore-ci`** — top-level navigation to other sem-ai commands
- **`sem-ai yaml_validate`** — call after every draft change
- **`sem-ai project create`** — bootstrap the project before first push
- **`sem-ai secret create` / `secret list`** — manage secrets discovered during translation
- **`sem-ai workflow_list` / `job_log`** — observe + debug the first runs

## Out of scope (v1)

- Migrating GitHub Actions Marketplace plugins beyond the `actions/*` core set
- Multi-platform matrices that span Linux+macOS+Windows (Semaphore doesn't run Windows agents in most orgs)
- GitOps-flavored deployment workflows that depend on GHA-specific tokens
- Auto-removing `.github/workflows/*` after migration — leave that to the user
