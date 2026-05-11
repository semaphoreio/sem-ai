---
name: semaphore-ci
description: Manage Semaphore CI/CD via sem-agent. Use when the user asks about CI status, pipeline failures, test results, deployments, secrets, or anything related to their Semaphore pipelines and workflows.
when_to_use: >
  Trigger on: "CI status", "pipeline failed", "why did CI fail", "test results",
  "deploy to staging", "rerun the pipeline", "what's flaky", "check the build",
  "show me the logs", "promote to production", "secrets", "notifications",
  "scheduled tasks", "deployment targets", "validate yaml", "project health"
allowed-tools: Bash(sem-agent *)
---

# Semaphore CI/CD — via sem-agent

`sem-agent` is a CLI that gives you full control over Semaphore CI/CD. Every command returns structured JSON. 77 commands covering projects, workflows, pipelines, jobs, tests, artifacts, secrets, deploys, and more.

## Setup

```bash
# Connect (one-time). Get your token at https://me.semaphoreci.com/account
sem-agent connect <your-org>.semaphoreci.com <your-api-token>

# Verify
sem-agent context show
```

## Self-orientation

```bash
sem-agent discover                  # Full capability map (77 commands + flags + examples)
sem-agent <any-command> --examples  # Usage examples for any command
```

## Quick reference

| Task | Command |
|------|---------|
| CI status | `sem-agent status --project <p> --branch <b>` |
| Why did CI fail? | `sem-agent diagnose <workflow-id>` |
| Project health | `sem-agent health --project <p>` |
| Job logs | `sem-agent job log <job-id>` |
| Test results | `sem-agent test summary --pipeline <id>` |
| Rerun workflow | `sem-agent workflow rerun <id>` |
| Rebuild failed only | `sem-agent rerun-failed <pipeline-id>` |
| Deploy to staging | `sem-agent pipeline promote <id> --target "Staging" --confirm` |
| Deploy and wait | `sem-agent promote-and-wait <id> --target "Staging" --confirm` |
| Validate YAML | `sem-agent yaml validate --file .semaphore/semaphore.yml` |
| Server diagnostics | `sem-agent troubleshoot workflow <id>` |
| List secrets | `sem-agent secret list` |
| Flaky tests | `sem-agent test flaky --project <p>` |
| Test locally in CI env | `sem-agent testbox warmup --project <p>` then `sem-agent testbox run --id <id> "cmd"` |

## Sub-skills — load for deeper context

For detailed workflows with step-by-step examples, load the relevant sub-skill:

- **Debugging failures** → load `debug-pipeline` — diagnosing, reading logs, fixing CI
- **Testing locally in CI** → load `testbox` — run tests in real Semaphore env before pushing
- **Deploying** → load `deploy` — promotions, deployment targets, deploy-and-wait
- **Test analysis** → load `test-intelligence` — test results, flaky detection, frameworks
- **Infrastructure** → load `manage-infra` — secrets, notifications, agents, tasks
- **Monitoring** → load `project-health` — health checks, pass rates, trends

## Safety

- `pipeline promote` requires `--confirm` to execute. Without it: dry-run preview only.
- `--override` bypasses promotion conditions — use with caution.
- Delete operations execute immediately.
- All output is JSON. Use `--format table` for human display.

## Output format

Success: JSON to stdout, exit 0.
Error: `{"error": true, "code": "not_found", "message": "...", "status": 404}` to stderr, exit 1.
