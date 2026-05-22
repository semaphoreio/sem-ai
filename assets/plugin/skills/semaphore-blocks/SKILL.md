---
name: semaphore-blocks
description: Explain how Semaphore pipelines are structured — blocks, tasks, jobs, dependencies, parallelism — and link to sem-ai commands for inspecting them. Use when the user asks about Semaphore pipeline structure, blocks, tasks, jobs, parallelism, dependencies, fan-out / fan-in, why a job is waiting, how to split work into parallel blocks, or where to put agent / prologue / epilogue / secrets / env_vars.
---

# Semaphore blocks, tasks, and jobs

## The model in three sentences

A Semaphore pipeline is a directed acyclic graph of **blocks**. Each block contains exactly one **task**, and the task contains one or more **jobs** that run in parallel on identical agents. Blocks declare `dependencies` on other blocks — that's what makes one block wait for another.

## Vocabulary

| Term | Where | Meaning |
|---|---|---|
| **Pipeline** | top-level (the YAML file) | The whole DAG. Owns the default `agent`, plus `global_job_config` for prologue/epilogue/env_vars/secrets shared across every block. |
| **Block** | item under `blocks:` | The dependency unit of the graph. Required: `name`, `task` (exactly one). Optional: `dependencies`, `skip`, `run` (conditional execution). |
| **Task** | inside a block | Execution-config wrapper for that block's jobs. Where `agent` override, `prologue`, `epilogue`, `secrets`, `env_vars` live — they apply to every job in this task. Required: `jobs`. |
| **Job** | item under `task.jobs:` | One sequence of `commands` (or `commands_file`) run by Semaphore. Multiple jobs in the same task run **in parallel**, scheduled as agents become available — order is not guaranteed. Jobs can carry `env_vars`, `secrets`, `matrix`, `parallelism`, `execution_time_limit`, `priority`; jobs cannot override `agent`. |
| **Promotion** | top-level `promotions:` | A separate pipeline triggered after the current one finishes (deploy gates, environment fan-out). Not a block — see the `semaphore-promotions` skill. |
| **after_pipeline** | top-level | A task that always runs once the pipeline reaches a terminal state — for test-results aggregation, notifications, cleanup. Same `task: { jobs: [...] }` shape; not a block in the dependency graph. |

## Where each setting lives — quick reference

| Setting | Pipeline level | Task level | Job level |
|---|---|---|---|
| **agent** (machine type + os_image) | ✅ default | ✅ overrides for this task | ❌ not allowed |
| **prologue** (runs before each job) | ✅ via `global_job_config` | ✅ overrides | ❌ |
| **epilogue** (runs after each job) | ✅ via `global_job_config` | ✅ overrides | ❌ |
| **env_vars** | ✅ via `global_job_config` | ✅ adds/overrides | ✅ adds/overrides for that job only |
| **secrets** (bound from Semaphore secret store) | ✅ via `global_job_config` | ✅ adds | ✅ adds for that job only |

So: **want different agents for different jobs? Put them in separate blocks**, each with its own `task:` agent override. There's no `agent:` you can set on an individual job — the block boundary is where agent changes.

## Parallel vs sequential — the two shapes that matter

### Parallel (recommended default)

```yaml
blocks:
  - name: Lint
    dependencies: []          # no deps → starts immediately
    task:
      jobs:
        - name: golangci-lint
          commands: [golangci-lint run ./...]

  - name: Test
    dependencies: []          # also starts immediately, parallel with Lint
    task:
      jobs:
        - name: unit
          commands: [go test ./...]
        - name: race           # parallel with `unit` (same task → same block)
          commands: [go test -race ./...]
```

Both blocks start at the same moment. Inside `Test`, the two jobs (`unit` + `race`) also run in parallel because they're in the same task.

### Sequential (when one block's output is the next block's input)

```yaml
blocks:
  - name: Build
    dependencies: []
    task:
      jobs:
        - name: compile
          commands: [go build -o bin/app .]

  - name: Smoke test
    dependencies: [Build]     # waits for Build to pass
    task:
      jobs:
        - name: run-bin
          commands: [./bin/app --version]
```

`Smoke test` does not start until `Build` is `passed`.

## Sharding one job across N parallel runs (`parallelism:`)

Inside a single job, `parallelism: N` spawns the job N times in parallel on N agents. Each run gets a unique pair of env vars:

| Env var | Value |
|---|---|
| `SEMAPHORE_JOB_INDEX` | 1-based index of this run (1..N) |
| `SEMAPHORE_JOB_COUNT` | Total runs (equals `N`) |

### Prefer the test runner's native shard flag

Most modern runners know how to slice their own suites — duration-balanced where possible, file-balanced otherwise. Pass `$SEMAPHORE_JOB_INDEX` / `$SEMAPHORE_JOB_COUNT` to the runner; let it pick the tests:

| Runner | Native flag |
|---|---|
| **Jest** (v28+) | `--shard=$SEMAPHORE_JOB_INDEX/$SEMAPHORE_JOB_COUNT` |
| **Vitest** | `--shard=$SEMAPHORE_JOB_INDEX/$SEMAPHORE_JOB_COUNT` |
| **Playwright** | `--shard=$SEMAPHORE_JOB_INDEX/$SEMAPHORE_JOB_COUNT` |
| **pytest** (with `pytest-split`) | `--splits=$SEMAPHORE_JOB_COUNT --group=$SEMAPHORE_JOB_INDEX` |
| **pytest** (with `pytest-shard`) | `--shard-id=$((SEMAPHORE_JOB_INDEX - 1)) --num-shards=$SEMAPHORE_JOB_COUNT` |
| **RSpec** (with `parallel_tests` gem) | `parallel_rspec --only-group $SEMAPHORE_JOB_INDEX --group-by runtime --groups $SEMAPHORE_JOB_COUNT` |
| **Knapsack Pro** | sets shard via its own env vars; works with Semaphore parallelism |
| **Cypress** | no native shard; use [`cypress-split`](https://github.com/bahmutov/cypress-split) plugin (`--env split=$SEMAPHORE_JOB_INDEX,splitCount=$SEMAPHORE_JOB_COUNT`) or Cypress Cloud `--parallel --record` if you have a record key |
| **Go** | no native shard; split by package list (manual) or use [`gotestsum --rerun-fails-report`](https://github.com/gotestyourself/gotestsum) plus a package-list filter |
| **Maven Surefire** | `mvn test -P parallel-shard -Dshard.index=$SEMAPHORE_JOB_INDEX -Dshard.count=$SEMAPHORE_JOB_COUNT` (config-dependent) |

Example — Jest sharded across 4 agents:

```yaml
- name: Jest
  parallelism: 4
  commands:
    - npm ci
    - npx jest --shard=$SEMAPHORE_JOB_INDEX/$SEMAPHORE_JOB_COUNT
```

Same shape works for Vitest, Playwright, pytest-split — only the flag changes.

### Fallback — manual file split when there's no native flag

For Cypress without `cypress-split`, Go, or any runner with no shard support, fall back to splitting the input list modulo N:

```yaml
- name: Cypress (no plugin)
  parallelism: 4
  commands:
    - SPECS=$(find cypress/e2e -name '*.cy.js' | awk "NR % $SEMAPHORE_JOB_COUNT == ($SEMAPHORE_JOB_INDEX - 1)")
    - npx cypress run --spec "$(echo $SPECS | tr '\n' ',' | sed 's/,$//')"
```

Manual split is uniform by file count, not by runtime — if one spec is 90% of wall-clock, this won't help. Prefer the native flag whenever available; most modern runners are time-balanced.

**Footgun**: your file glob must match what the runner would discover on its own. Easy to miss:
- nested subdirectories — `cypress/e2e/*.cy.js` only matches top-level files, ignoring `cypress/e2e/plugins/*.cy.js`. Use `find cypress/e2e -name '*.cy.js'` or `cypress/e2e/**/*.cy.js` (with globstar).
- alternative suffixes — `.spec.ts` vs `.test.ts` vs `.cy.ts`; check the framework's actual discovery pattern.
- excluded files in framework config — `cypress.config.js`'s `excludeSpecPattern`, `vitest.config.ts`'s `exclude`, `jest`'s `testPathIgnorePatterns`. Your manual glob doesn't know about these; the runner does.

Native shard flag delegates discovery to the runner and can't desync. Every reason to prefer it.

### Is it actually a wall-clock win? (the cost-benefit check)

Parallelism multiplies agent-minutes by N. It only multiplies *value* by N when the per-shard fixed cost (install + setup + checkout + cache restore + cache store) doesn't dominate. Run the math before applying:

```
single-shard wall = setup + tests
N-shard wall      = setup + (tests / N)     # tests/N assumes balanced split
agent-time used   = N × (setup + tests / N) = N × setup + tests
```

So parallelism is a wall-clock win **only when `tests` is large compared to `setup`**. Two concrete shapes:

| Pipeline | Single | 4-way | Verdict |
|---|---|---|---|
| 2m setup + 30s tests | 2m30s wall | 4 × (2m + 7.5s) = 8m30s agent for 2m07s wall | tiny gain, 3.4× cost — **not worth it** |
| 10s cached setup + 4m tests | 4m10s wall | 4 × (10s + 1m) = 4m40s agent for 1m10s wall | **3.5× faster wall**, ~12% more agent-min |
| 10s cached setup + 30s tests | 40s wall | 4 × (10s + 7.5s) = 70s agent for 17s wall | **2.4× faster** — fine but small absolute win |
| 2m setup + 8m tests | 10m wall | 4 × (2m + 2m) = 16m agent for 4m wall | **2.5× faster**, 60% more agent-min — usually worth it |

**Safe-to-shard checklist** (all should be true):

- [ ] **Tests are the bottleneck** — the test command alone is > ~1 min wall-clock. If the slow part is `npm install`, shard does not help.
- [ ] **Splittable** — runner has a native shard flag (Jest / Vitest / Playwright / pytest-split / etc.) OR specs are independently runnable (no shared mutable state).
- [ ] **Setup is cached or genuinely cheap** — dep cache, language version, browser binaries, etc. all restore quickly. The per-shard "cold" install must not eat the savings.
- [ ] **Split balances reasonably** — most native shard flags balance by file or by previous duration. If your suite has one 90% spec and many tiny ones, the unbalanced shard cancels the win.

If any of those fails, **don't shard** — flag it in the PR body as "considered, not applied because X" rather than silently apply.

**When NOT to use parallelism at all**:
- Different stack versions (Go 1.25 vs 1.26) — use a matrix or separate blocks instead.
- Independent concerns (Lint + Test + Vet) — they're already separate jobs in one task. Parallelism is for splitting *one* logical job into N copies of itself, not for fan-out across distinct work.

**Combine with sibling jobs**: a block can mix `parallelism:` jobs with regular jobs. The parallelism count is per-job, not per-task.

## Block status aggregation (gotcha)

A block's status is the **max-failure** of its jobs. If a block has 5 jobs and one fails:
- Block status → `failed`
- All other (passing) jobs still appear with their individual `passed` state in the per-job view
- Downstream blocks that `dependencies: [<this block>]` will NOT start

This trips agents who see "Vitest passed" in one place and "Block failed" elsewhere and conclude something's wrong. Both are true. **Block = max-failure of its jobs; check per-job results, not just block-level.**

## Explicit `dependencies:` — all-or-none (gotcha)

Once **any** block in the pipeline declares `dependencies:`, **every** block must declare it (use `dependencies: []` for blocks with no upstream). Mixing implicit + explicit is a yaml validation error:

```
error: cannot mix explicit and implicit dependencies in the same pipeline
```

Convention: declare `dependencies:` on every block, even roots. Costs one line per root block; saves the surprise.

```yaml
blocks:
  - name: Quality
    dependencies: []          # explicit empty — must be declared
    task: { ... }

  - name: Security
    dependencies: []          # explicit empty
    task: { ... }

  - name: Build
    dependencies: [Quality]   # actual dependency
    task: { ... }
```

## `auto_cancel` — cancel stale runs on the same branch

Pipeline-level setting that controls what happens when a new push arrives on a branch that already has an in-flight pipeline. Without `auto_cancel`, both runs continue — agent-min wasted on a commit nobody cares about anymore.

```yaml
auto_cancel:
  running:
    when: "branch != 'main' AND branch != 'master'"
```

- `running.when` — cancel **in-progress** runs when the predicate is true at the time of the new push.
- `queued.when` (less common) — also cancel runs that haven't started yet. Use both together if your queue tends to back up.

**Recommended default**: cancel on every branch EXCEPT `main` / `master`. Feature branches care only about the latest commit; main/master runs may be tied to deploy promotions or release tags, so they should always finish.

Apply once at the pipeline root — not per block.

If a project intentionally needs concurrent runs on the same branch (e.g. matrix builds across feature flags driven from external triggers), omit `auto_cancel` and document why.

## When to put work in the same task vs different blocks

| Use same task (multiple jobs in one block) | Use separate blocks |
|---|---|
| Same setup steps (prologue) | Different agent / hardware |
| Same agent config | Different stack version (Go 1.25 vs 1.26) |
| Cheap parallelism without coordination | Distinct concerns where you want fail-fast independence |
| Inside a fan-out test matrix | One can fail without others wasting time |

Rule of thumb: split into separate blocks when **the agent or dependencies differ**, or when **you want a failure in one to not block the others** running. Same-task parallelism is for cheap parallelism without coordination overhead.

## Inspecting a real pipeline with sem-ai

To see the dependency graph of a specific pipeline:

```
sem-ai pipeline topology <pipeline-id>
```

To see block-level results (which blocks passed/failed, with timing):

```
sem-ai pipeline show <pipeline-id>
```

To find pipeline IDs for the current branch:

```
sem-ai status --project <name> --branch <branch>
```

To pre-flight YAML syntax before pushing:

```
sem-ai yaml validate --file .semaphore/semaphore.yml
```

### Polling a pipeline to terminal state — `result`, not `state`

When waiting for a pipeline to finish (e.g. background `until` loops watching first-run completion), check the **`result`** field, not `state`:

- `state` — `RUNNING`, `STOPPING`, `DONE` — has intermediate values that show up on nested objects (e.g. each block has its own `state: DONE` once the block finishes, while the parent pipeline is still running).
- `result` — `PASSED`, `FAILED`, `STOPPED`, `CANCELED` — only populated when the **pipeline itself** terminates.

Polling `state == "DONE"` will fire false positives on inner block transitions. Polling `result != ""` (or `result in ("PASSED","FAILED","STOPPED","CANCELED")`) only fires when the pipeline is genuinely terminal.

```bash
# Correct
until result=$(sem-ai pipeline show "$id" --jq '.pipeline.result // ""') && [ -n "$result" ]; do
  sleep 30
done

# WRONG — fires on intermediate block-state="done" before pipeline ends
until sem-ai pipeline show "$id" --jq '.pipeline.state' | grep -q done; do
  sleep 30
done
```

## Common patterns

### Fan-out, fan-in (Quality → Build)

```yaml
blocks:
  - name: Quality
    dependencies: []
    task:
      jobs:
        - { name: Lint,     commands: [golangci-lint run ./...] }
        - { name: Test,     commands: [go test ./...] }
        - { name: Vet,      commands: [go vet ./...] }

  - name: Build
    dependencies: [Quality]                # waits for Quality
    task:
      jobs:
        - { name: linux-amd64,  commands: [GOOS=linux GOARCH=amd64 go build .] }
        - { name: darwin-arm64, commands: [GOOS=darwin GOARCH=arm64 go build .] }
```

Quality runs 3 jobs in parallel. Build waits for Quality, then runs 2 build jobs in parallel.

### True parallel (no fan-in)

```yaml
blocks:
  - name: Quality
    dependencies: []
    task: { jobs: [...] }

  - name: Security
    dependencies: []      # parallel with Quality
    task: { jobs: [{ name: trivy, commands: [trivy fs .] }] }

  - name: Build snapshot
    dependencies: []      # parallel with everything; cross-compile validation doesn't need Quality to pass first
    task: { jobs: [{ name: build, commands: [goreleaser build --snapshot] }] }
```

All three blocks start at once. Wall-clock is `max(Quality, Security, Build)` instead of `Quality + Security + Build`. Loosening unneeded dependencies often cuts ~25%+ off wall-clock — branch protection still blocks merge on red, so safety is preserved.

### Different agent per job (via separate blocks)

```yaml
agent:                                   # pipeline default
  machine: { type: e2-standard-2, os_image: ubuntu2204 }

blocks:
  - name: Lint on default agent
    dependencies: []
    task:
      jobs:
        - { name: golangci-lint, commands: [golangci-lint run ./...] }

  - name: GPU smoke test on beefier box
    dependencies: []
    task:
      agent:                             # task-level override → applies to all jobs in this block
        machine: { type: g1-standard-4, os_image: ubuntu2204 }
      jobs:
        - { name: cuda-smoke, commands: [./gpu-smoke.sh] }
```

To give a *single* job a different agent, give it its own block. There's no `agent:` you can set on a job directly.

## Diagnostic playbook for the agent

**"My job is stuck waiting"** → run `sem-ai pipeline topology <id>` to see what it depends on. If the upstream block is still running, that's normal. If the upstream block already passed, check `sem-ai pipeline show <id>` for the block's actual state.

**"My pipeline is slow"** → look for serial dependencies that don't need to be serial. Use `sem-ai analytics duration --project <name>` to see per-phase breakdown. Often Build can lose its dependency on Lint/Test.

**"I want to add a new lint check"** → add another job inside the existing `Quality` block's `task.jobs` (parallel by default, shares prologue) rather than spinning a new block. Less YAML, faster.

**"I want different agents for different work"** → use separate blocks; each block's task can carry its own `agent:` override.

**"My env var isn't visible inside the job"** → check the scope. Env vars at `global_job_config` apply everywhere; at `task.env_vars` apply to that block's jobs; at `task.jobs[i].env_vars` apply to that one job. Same precedence as you'd expect (job > task > global).

## Boundaries

- This skill explains **structure**. For the **deploy** side (promotions, deployment targets, gates) use the `semaphore-promotions` skill.
- For **toolbox CLIs used inside jobs** (`checkout`, `cache`, `artifact`, `retry`, `sem-version`, `sem-service`) see `semaphore-toolbox`.
- For **publishing test reports** (epilogue placement, per-framework JUnit configs, pipeline aggregation) see `semaphore-test-results`.
- For **why a job failed**, route to `sem-ai diagnose <workflow-id>` — that's the canonical failure-analysis entry point.
