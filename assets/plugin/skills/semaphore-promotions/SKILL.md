---
name: semaphore-promotions
description: Explain Semaphore promotions — chained pipelines for deploys, gates, and environment fan-out — and link to sem-ai commands for triggering and inspecting them. Use when the user asks about Semaphore promotions, deploys, deployment targets, auto-promote, parameterized promotions, staging vs production gates, how to chain pipelines, or environment fan-out.
---

# Semaphore promotions

## The model in three sentences

A **promotion** triggers a *separate pipeline* (declared in another YAML file) after the current pipeline finishes. Promotions are how Semaphore models deploys, manual gates, multi-environment fan-out, and any "step 2 that runs after step 1 succeeds." They live at the top level of the pipeline YAML — they are **not** blocks in the current pipeline's DAG.

## Vocabulary

| Term | Where | Meaning |
|---|---|---|
| **promotion** | top-level `promotions:` item | One entry that points at another pipeline file and declares when/how it can be triggered. |
| **pipeline_file** | inside a promotion | Relative path to the target pipeline YAML, resolved from the current pipeline's directory (`.semaphore/semaphore.yml` + `pipeline_file: deploy.yml` → `.semaphore/deploy.yml`). |
| **auto_promote** | inside a promotion | Optional `when:` expression that fires the promotion automatically when the parent pipeline finishes and conditions hold. |
| **parameters.env_vars** | inside a promotion | Optional named inputs the user fills at promote time (or that auto_promote passes through). Each is an env var the promoted pipeline sees. |
| **deployment_target** | inside a promotion | Optional reference (by name) to a configured Deployment Target. Permissions and rules on the target gate who can promote — independent of repo permissions. |

## Promotion vs block — the load-bearing distinction

| | Block | Promotion |
|---|---|---|
| Where in graph | Node inside the current pipeline | A new pipeline triggered after the current one |
| When it runs | When dependencies pass (auto, same workflow) | When auto_promote condition fires, OR manually clicked, OR via API |
| What fails the parent | Yes — block failure fails the workflow | No — promotion target failures are separate runs |
| Has its own agent / blocks / after_pipeline | No — uses the parent pipeline's | Yes — the target pipeline file is fully independent |
| Used for | Compile, lint, test, build | Deploy, release, environment fan-out, manual gates |

If the answer to "should this run *only when CI is green*?" is yes, you want a promotion. If it should run as part of CI itself, you want a block.

## Worked patterns

### Auto-promote on main passing

```yaml
# .semaphore/semaphore.yml
blocks: [...]

promotions:
  - name: Deploy to staging
    pipeline_file: deploy-staging.yml
    auto_promote:
      when: "branch = 'main' AND result = 'passed'"
```

`deploy-staging.yml` fires automatically when CI passes on `main`. Other branches / failed builds don't trigger it.

### Manual promotion to production with a parameter

```yaml
promotions:
  - name: Push to production
    pipeline_file: deploy-prod.yml
    parameters:
      env_vars:
        - name: RELEASE_TAG
          description: Tag to deploy (e.g. v1.2.3)
          required: true
        - name: ROLLOUT
          description: Rollout mode
          required: true
          default_value: canary
          options:
            - canary
            - immediate
```

No `auto_promote` → user clicks the promote button in the Semaphore UI (or runs `sem-ai pipeline promote`) and fills in the form. `deploy-prod.yml` sees `RELEASE_TAG` and `ROLLOUT` as env vars.

### Deployment-target-gated promotion

```yaml
promotions:
  - name: Push to production
    pipeline_file: deploy-prod.yml
    deployment_target: Production         # references a Deployment Target by name
```

The `Production` target carries its own RBAC (which users/roles can promote), branch/tag restrictions, secrets binding, and env_vars. Repo-write permission is not enough — the user must also satisfy the target's `subject_rules`. See `sem-ai deploy show <target-id>` for what's configured.

### Chained promotions (staging → production)

```yaml
# .semaphore/semaphore.yml          ← CI pipeline
promotions:
  - name: Deploy staging
    pipeline_file: deploy-staging.yml
    auto_promote: { when: "branch = 'main' AND result = 'passed'" }

# .semaphore/deploy-staging.yml     ← staging pipeline
blocks: [...]
promotions:
  - name: Deploy production
    pipeline_file: deploy-prod.yml
    deployment_target: Production    # manual + RBAC-gated
```

Tree-shaped workflows: CI → staging (auto) → production (manual + gated). Each tier is its own pipeline file with its own blocks, agent, after_pipeline.

## `auto_promote.when` — the conditions DSL

Common keys: `result`, `branch`, `tag`, `pull_request`, `change_in`.

Examples:

| Goal | `when:` expression |
|---|---|
| Only on main, only when green | `branch = 'main' AND result = 'passed'` |
| Only on tag pushes matching `v*` | `tag =~ '^v[0-9].*'` |
| Only when a specific directory changed | `change_in('/services/api/') AND branch = 'main'` |
| Never auto — manual only | omit `auto_promote` entirely |

## Pre-flight checks — the other gate

Promoted pipelines are subject to **organization and project pre-flight checks** in addition to the promotion's own `auto_promote` / `deployment_target` rules. Pre-flight checks run during *pipeline initialization* — before any block starts — and if any check fails, **the pipeline never runs at all**.

| Pre-flight check | Where configured | Scope |
|---|---|---|
| Organization-level | Org Settings → Initialization jobs → Pre-flight checks | Every pipeline in the org (CI and every promoted pipeline) |
| Project-level | Project Settings → Pre-flight checks | Every pipeline in that project |

Important:

- Pre-flight checks are **not in pipeline YAML**. They live in Semaphore Settings (UI / API). YAML changes won't disable them.
- Checks are shell commands run in an init job. Can bind secrets, have standard env vars (`SEMAPHORE_PROJECT_NAME`, `SEMAPHORE_GIT_BRANCH`, etc.).
- The `SEMAPHORE_PIPELINE_PROMOTION` env var is set when the check runs as part of a *promoted* pipeline init — use it to skip-or-tighten checks for promotions specifically.
- Failure mode: hard fail at init. The pipeline shows up with a failed initialization job and no blocks ever scheduled.

Common uses:

| Goal | Pre-flight check command |
|---|---|
| Block all promotions outside business hours | `if [ "$SEMAPHORE_PIPELINE_PROMOTION" = "true" ] && [ $(date +%H) -ge 18 ]; then exit 1; fi` |
| Require a specific approval label on the originating PR | shell script that queries the SCM API + checks the label |
| Refuse promotions when a downstream service is in maintenance mode | curl a status endpoint + grep for `MAINTENANCE` |

Two non-obvious behaviors for the agent to remember:

- **A promotion that "didn't fire" might have started but failed at pre-flight.** Check `sem-ai pipeline show <promoted-pipeline-id>` for an init-job failure with `result: failed` and a near-zero block count.
- **Disabling a pre-flight check is an org/project setting change**, not a YAML edit. The user must do it in the Semaphore UI (or via API).

## Inspecting and triggering with sem-ai

| Want | Command |
|---|---|
| List deployment targets on a project | `sem-ai deploy targets --project <name>` |
| Show target config (rules + bindings) | `sem-ai deploy show <target-id>` |
| Show recent deploys for a target | `sem-ai deploy history <target-id>` |
| Manually trigger a promotion | `sem-ai pipeline promote <pipeline-id> --target <name>` |
| Block a target temporarily | `sem-ai deploy deactivate <target-id>` |
| Re-enable | `sem-ai deploy activate <target-id>` |
| Create a new target | `sem-ai deploy create --name <n> --url <u> [...rules]` |

## Diagnostic playbook for the agent

**"My deploy didn't fire after green CI"** → check the promotion's `auto_promote.when` expression. Run `sem-ai pipeline show <ci-pipeline-id>` and confirm `branch` / `result` actually match what the condition expects.

**"User can't promote — permission denied"** → it's the deployment_target's `subject_rules`, not repo permissions. `sem-ai deploy show <target-id>` shows the rules; add the user/role via `sem-ai deploy update` (or the UI).

**"How do I deploy a specific version manually?"** → if the pipeline has a parameterized promotion: `sem-ai pipeline promote <pipeline-id> --target <name>` and pass the parameter values. Otherwise: tag the desired commit and let the auto_promote on tag fire.

**"What changed between two deploys?"** → `sem-ai deploy history <target-id>` lists each promotion with workflow IDs and commits. Pick two SHAs and `git log A..B`.

**"Promotion shows as failed with no block output"** → likely a **pre-flight check** failure at init time. The pipeline never reached the blocks. Inspect the init job for the failure message; remediation is in Semaphore Settings, not YAML.

## Boundaries

- For pipeline structure (what blocks/tasks/jobs/agents are inside the CI pipeline OR the deploy pipeline), use the `semaphore-blocks` skill.
- For the full YAML anatomy (version, name, agent, global_job_config, after_pipeline, all the moving parts), use the `semaphore-pipeline-yaml-anatomy` skill (forthcoming).
- For *what* the deployed pipeline does (build artifacts, container push, terraform, etc.), that's your project's code — out of Semaphore's structural scope.
