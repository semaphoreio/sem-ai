---
name: semaphore-preflight
description: Diagnose and configure Semaphore pre-flight checks — the organization- and project-level commands that run at pipeline initialization, before any block. Use when a pipeline or promotion fails at init with no block output, when the user asks why nothing ran, or when they want to read, add, change, or remove a gate that applies to every workflow in an organization or project.
---

# Semaphore pre-flight checks

## The model in three sentences

A **pre-flight check** is a list of shell commands Semaphore runs in an init job at the start of *every* workflow in a scope, before any block is scheduled. There are two scopes — one check per organization and one per project — and both run, so a pipeline passes only if neither fails. A non-zero exit stops the pipeline there, which is why the symptom is a failed pipeline with **no block output at all**.

| Scope | Applies to | Read it with |
|---|---|---|
| Organization | Every pipeline in the org, CI and promotions alike | `sem-ai pfc show` |
| Project | Every pipeline in that one project | `sem-ai pfc show --project <name>` |

Pre-flight checks are **not** in `.semaphore/semaphore.yml`. Editing YAML cannot disable them.

## Diagnose — is this actually a pre-flight failure?

The tell is a pipeline that failed while its block list is empty or never started.

```bash
# 1. What failed, and did any block run?
sem-ai diagnose <workflow-id>
sem-ai pipeline show <pipeline-id>     # blocks: [] or every block still pending

# 2. Server-side reason for a pipeline that never got going.
sem-ai troubleshoot pipeline <pipeline-id>

# 3. If an init job is listed, its log holds the check's own output.
sem-ai job list --states FINISHED
sem-ai job log <job-id>
```

Rule of thumb:

| Symptom | Likely cause | Next step |
|---|---|---|
| Failed pipeline, zero blocks, no test results | Pre-flight check failed | `sem-ai pfc show` then `sem-ai pfc show --project <name>` |
| Pipeline stuck `initializing` | Invalid pipeline YAML | `sem-ai yaml validate --file .semaphore/semaphore.yml` |
| Blocks ran, one failed | Ordinary job failure | `debug-pipeline` skill |
| Fails only on promoted pipelines | Check keyed on `SEMAPHORE_PIPELINE_PROMOTION` | read the commands in `sem-ai pfc show` |

Check **both** scopes before concluding a project is clean — an org-level check fails project pipelines that have no check of their own.

## Read the current check

```bash
sem-ai pfc show                        # organization-wide
sem-ai pfc show --project my-project   # one project
```

Returns the `commands`, the `secrets` they are allowed to use, the `agent` the init job runs on, and who last changed it (`requester_id`, `updated_at`). "No pre-flight check configured for this organization/project" means the scope has none — that is a valid state, not an error to work around.

## Change it

`apply` **replaces the whole check** — it does not merge. Whatever you send becomes the check in full, and it takes effect on the next workflow in that scope, for everyone.

So: read first, show the user the difference, then apply.

```bash
# 1. What is there now?
sem-ai pfc show --project my-project

# 2. Apply the full intended check (commands run in the order given).
sem-ai pfc apply --project my-project \
  --command checkout \
  --command './scripts/security-gate.sh' \
  --secret scanner-token

# 3. Confirm, then prove it on a real run.
sem-ai pfc show --project my-project
sem-ai workflow run --project my-project
```

Pin the init job's agent when the commands need a particular image:

```bash
sem-ai pfc apply --command './scripts/gate.sh' \
  --machine-type e2-standard-2 --os-image ubuntu2204
```

For anything longer than a couple of commands, keep the spec in a file and apply that — it is reviewable and re-appliable:

```yaml
# pfc.yml
commands:
  - checkout
  - ./scripts/security-gate.sh
secrets:
  - scanner-token
agent:
  machine_type: e2-standard-2
  os_image: ubuntu2204
```

```bash
sem-ai pfc apply --project my-project --from-file pfc.yml
```

`--from-file` accepts YAML or JSON and cannot be combined with `--command` / `--secret` / `--machine-type` / `--os-image`. Scope always comes from `--project`, never from the file.

## Remove it

```bash
sem-ai pfc delete --project my-project   # drop the project check
sem-ai pfc delete                        # drop the organization-wide check
```

Deleting one scope leaves the other in force. If a project's pipelines still fail at init after deleting the project check, the org-level check is the one failing them.

## Writing a check that is easy to live with

- Commands run in a plain init job, so `checkout` first if the check reads the repo.
- Standard env vars are available — `SEMAPHORE_PROJECT_NAME`, `SEMAPHORE_GIT_BRANCH`, and `SEMAPHORE_PIPELINE_PROMOTION` (set when the init is for a promoted pipeline, useful for gating deploys only).
- Exit non-zero **only** for conditions the user genuinely wants to block. A flaky network call in a pre-flight check takes down every pipeline in the scope, including the one that would fix it.
- Anything that needs credentials must name them in `secrets`; the check cannot reach a secret it was not given.

## Errors you will see

| Response | Meaning |
|---|---|
| `no pre-flight check configured for this organization/project` | Nothing set at that scope |
| `permission denied: this requires the ...pre_flight_checks.manage permission` | The token can read but not change checks — ask an org owner |
| `...feature is not enabled for your organization` | Pre-flight checks are not on this plan/organization |
| `invalid pre-flight check: ...` | The server rejected the spec — the message names the field |

## Boundaries

- Failures *inside* a block are ordinary job failures — use the `debug-pipeline` skill.
- Promotion gating rules (`auto_promote`, deployment targets, who may promote) are the `semaphore-promotions` skill; a pre-flight check is a second, independent gate on top of them.
- Block, task, and job structure inside the pipeline is the `semaphore-blocks` skill.
