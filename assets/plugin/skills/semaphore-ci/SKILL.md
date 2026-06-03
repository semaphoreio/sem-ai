---
name: semaphore-ci
description: Manage Semaphore CI/CD via sem-ai. Use when the user asks about CI status, pipeline failures, test results, deployments, secrets, notifications, scheduled tasks, deployment targets, project health, or anything related to their Semaphore pipelines and workflows — e.g. "CI status", "pipeline failed", "why did CI fail", "deploy to staging", "rerun the pipeline", "what's flaky", "check the build", "show me the logs", "promote to production", "validate yaml".
allowed-tools: Bash(sem-ai *)
---

# Semaphore CI/CD — via sem-ai

`sem-ai` is a CLI that gives you full control over Semaphore CI/CD. Every command returns structured JSON. 77 commands covering projects, workflows, pipelines, jobs, tests, artifacts, secrets, deploys, and more.

## Setup

```bash
# Connect (one-time). Get your token at https://me.semaphoreci.com/account
sem-ai connect <your-org>.semaphoreci.com <your-api-token>

# Verify
sem-ai context show
```

## Self-orientation

```bash
sem-ai discover                  # Full capability map (77 commands + flags + examples)
sem-ai <any-command> --examples  # Usage examples for any command
```

## Quick reference

| Task | Command |
|------|---------|
| CI status | `sem-ai status --project <p> --branch <b>` |
| Why did CI fail? | `sem-ai diagnose <workflow-id>` |
| Project health | `sem-ai health --project <p>` |
| Job logs | `sem-ai job log <job-id>` |
| Test results | `sem-ai test summary --pipeline <id>` |
| Rerun workflow | `sem-ai workflow rerun <id>` |
| Rebuild failed only | `sem-ai rerun-failed <pipeline-id>` |
| Deploy to staging | `sem-ai pipeline promote <id> --target "Staging" --confirm` |
| Deploy and wait | `sem-ai promote-and-wait <id> --target "Staging" --confirm` |
| Validate YAML | `sem-ai yaml validate --file .semaphore/semaphore.yml` |
| Server diagnostics | `sem-ai troubleshoot workflow <id>` |
| List secrets | `sem-ai secret list` |
| Flaky tests | `sem-ai test flaky --project <p>` |
| Test locally in CI env | `sem-ai testbox warmup --project <p>` then `sem-ai testbox run --id <id> "cmd"` |
| Watch CI after a push | `git push`, then `sem-ai watch <workflow-id>` (see `watch-after-push`) |

## Project detection

`sem-ai status`, `workflow list`, `health`, `diagnose`, `open`, and `analytics` auto-detect the project from the `origin` git remote (ssh or https) when `--project` is omitted — no need to pass it inside a checkout.

Caveat: detection matches the remote URL against project `repo_url` and returns the **first match**. If several Semaphore projects point at the same git remote, it may pick the wrong one — pass `--project <name>` explicitly for multi-project repos. (To target a specific run regardless, filter `workflow list` by `commit_sha`; see the `watch-after-push` skill.)

## Sub-skills — load for deeper context

For detailed workflows with step-by-step examples, load the relevant sub-skill:

- **Debugging failures** → load `debug-pipeline` — diagnosing, reading logs, fixing CI
- **Testing locally in CI** → load `testbox` — run tests in real Semaphore env before pushing
- **Deploying** → load `deploy` — promotions, deployment targets, deploy-and-wait
- **Test analysis** → load `test-intelligence` — test results, flaky detection, frameworks
- **Infrastructure** → load `manage-infra` — secrets, notifications, agents, tasks
- **Monitoring** → load `project-health` — health checks, pass rates, trends
- **After a push** → load `watch-after-push` — find the run for your commit and watch it to completion

## Safety

- `pipeline promote` requires `--confirm` to execute. Without it: dry-run preview only.
- `--override` bypasses promotion conditions — use with caution.
- Delete operations execute immediately.
- All output is JSON. Use `--format table` for human display.

## Output format

Success: JSON to stdout, exit 0.
Error: `{"error": true, "code": "not_found", "message": "...", "status": 404}` to stderr, exit 1.
