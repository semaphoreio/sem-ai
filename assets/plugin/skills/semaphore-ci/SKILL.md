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
| CI status | `sem-ai status` (`--project`/`--branch` auto-detect; `--pr N` resolves a PR's workflow) |
| Did my push pass? / watch CI to green | `sem-ai status` (or `until sem-ai status --exit-code; do sleep 20; done`); see `watch-after-push` |
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
| Flaky tests | `sem-ai test flaky` (`--project` optional; auto-detects) |
| Test locally in CI env | `sem-ai testbox warmup` (`--project` optional) then `sem-ai testbox run --id <id> "cmd"` |
| Watch CI after a push | `git push`, then `sem-ai watch <workflow-id>` (see `watch-after-push`) |

For the is-it-green check, prefer `sem-ai status` — it keeps the failure drill (`sem-ai diagnose <workflow-id>`) one tool away. Your git host's own checks (`gh pr checks` on GitHub, or the GitLab/Bitbucket equivalent) mirror the same Semaphore result and are a fine fallback when sem-ai isn't connected — Semaphore connects to any of those hosts.

## Project detection

`--project` is optional on all repo-scoped commands (status, workflow list/run, pipeline list, deploy targets/create, task list/create, test flaky, diagnose, health, open, analytics) — it auto-detects from the `origin` git remote (ssh or https) when omitted, so there's no need to pass it inside a checkout. `--branch` likewise auto-detects from HEAD; `sem-ai status` pins the current HEAD commit (`"matched_by":"commit_sha"`, falling back to `"latest_on_branch"`). Pass `--project`/`--branch` only to override.

Caveat: if the repo maps to **multiple** Semaphore projects, `sem-ai status` returns all of them (`"multiple_projects": true`) instead of guessing — pass `--project <name>` to pick one. (To target a specific run regardless, filter `workflow list` by `commit_sha`; see the `watch-after-push` skill.)

## Organization (context) selection

`~/.sem.yaml` can hold many contexts, one per organization, and `active-context` names the one commands use by default. That key is shared mutable state: `context switch`, `connect`, and `signin` all write it, so another session or agent on the same machine can move it between two of your commands and the second one silently runs against the wrong organization.

With more than one context, pin the organization instead of reading the shared key:

```bash
sem-ai --context myorg_semaphoreci_com status   # this invocation only
export SEM_CONTEXT=myorg_semaphoreci_com        # every later call in this shell
sem-ai context list                             # names and which one is active
```

Pin even on a machine with one context: another session's `connect` or `signin` adds a second and makes it active, so being alone in the file is not a property that survives the session.

Resolution order: `--context` > `SEM_CONTEXT` > `SEMAPHORE_HOST`/`SEMAPHORE_API_TOKEN` > `active-context`. A named context fully shadows the credential env vars, so its host and token always travel together. An unknown name fails immediately and lists what is available. Neither selector writes to `~/.sem.yaml`.

Over MCP, `context` is a parameter on every tool — pass it per call. `sem-ai mcp --context <name>` pins a whole server, and a per-call `context` still overrides it.

Rules of thumb:

- Don't run `context switch` to set up for a later command. It mutates the shared key for every session on the machine, and it does not reliably persist across separate shell invocations in an agent harness anyway. Pin instead.
- Keep using whichever context was already active unless the user asks for a different organization — pin its name explicitly rather than assuming it will still be active later.
- `context switch` is for a human deliberately changing their default, not for scoping one command.
- Onboarding a new organization works under a pin: `connect`, `signin`, `context switch`, and `context list` ignore the selectors, so naming a context that does not exist yet does not block the command that creates it. The selector is ignored rather than applied — `connect` names the context after its host, so `--context neworg connect neworg.semaphoreci.com TOKEN` creates `neworg_semaphoreci_com`; re-pin to that name afterwards. Giving `connect` a `<host>` plus a selector resolving to a different host is refused, not guessed.
- Onboarding happens on the command line: `connect` and `signin` are deliberately not MCP tools (a device flow would hold the server's lock and hide its one-time code; `connect`'s two positional arguments cannot be encoded as tool arguments). Run them in a shell, then use the context they created.
- `context list` marks the file's active context, and each row's `pinned` says which one the current invocation selected; `context show` reports what this invocation resolves to. A selector that resolves to a host different from the one `connect`/`signin` was given is refused rather than silently ignored.
- Writes to `~/.sem.yaml` (`connect`, `signin`, `context switch`) replace the whole file, so two of them overlapping is last-writer-wins for the entire config. Sequence them; don't run them concurrently from several sessions.

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
