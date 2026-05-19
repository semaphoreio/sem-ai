---
name: semaphore-blocks
description: Explain how Semaphore pipelines are structured — blocks, tasks, jobs, dependencies, parallelism — and link to sem-ai commands for inspecting them.
trigger: user asks about Semaphore pipeline structure, blocks, tasks, jobs, parallelism, dependencies, fan-out / fan-in, why a job is waiting, how to split work into parallel blocks, where to put agent / prologue / epilogue / secrets / env_vars
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

- This skill explains **structure**. For the **deploy** side (promotions, deployment targets, gates), use the `semaphore-promotions` skill (forthcoming).
- For **what the agent runs inside a job** (sem-version, cache restore, secrets binding, after_pipeline shape), see the `semaphore-pipeline-yaml-anatomy` skill (forthcoming).
- For **why a job failed**, route to `sem-ai diagnose <workflow-id>` — that's the canonical failure-analysis entry point.
