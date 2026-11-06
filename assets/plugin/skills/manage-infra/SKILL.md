---
name: manage-infra
description: Manage Semaphore infrastructure — secrets, notifications, agent types, scheduled tasks, artifacts.
user-invocable: false
---

# Infrastructure Management

## Secrets
```bash
sem-ai secret list [--project <p>]              # org or project level
sem-ai secret show <name> [--project <p>]
sem-ai secret create <name> --env KEY=VALUE [--project <p>]
sem-ai secret update <name> --env KEY=NEW [--project <p>]
sem-ai secret delete <name> [--project <p>]
```

## Notifications
```bash
sem-ai notification list
sem-ai notification show <name>
sem-ai notification delete <name>
```

## Self-hosted agents
```bash
sem-ai agent types                  # list agent types
sem-ai agent show <type-name>       # type details
sem-ai agent list --type <name>     # list agents of a type
sem-ai agent delete <type-name>     # delete type
```

## Scheduled tasks
```bash
sem-ai task list --project <p>
sem-ai task show <id>               # details + recent triggers + skip_*_notifications flags
sem-ai task run <id>                # trigger now
sem-ai task delete <id>

# Parameterized tasks: pass parameters, branch, and pipeline file.
# Parameters go to the task's run_now as a map; branch/pipeline-file pick
# the ref + YAML the task pipeline runs.
sem-ai task run <id> --param KEY=VALUE [--param ...] \
  [--branch <ref>] [--pipeline-file <path>]

# Create with parameter definitions: bare NAME = required,
# NAME=DEFAULT = optional with a default value.
# The skip flags silence Git provider commit statuses for pipelines the task
# starts on schedule / manually (Run now, API, CLI); reruns included.
# Push/PR pipelines always report. Changes take up to 5 min to apply.
sem-ai task create <name> --branch main --file <path> [--cron "<expr>"] \
  [--param-def NAME] [--param-def NAME=DEFAULT] \
  [--skip-scheduled-run-notifications] [--skip-manual-run-notifications]

# Change the skip flags on an existing task (keeps its ID and history).
# Each flag takes true/false; omit it to keep the current value.
sem-ai task update <id> [--skip-scheduled-run-notifications true|false] \
  [--skip-manual-run-notifications true|false]
```

## Artifacts

An artifact store is a tree. A bare `list` shows only its top level, where
entries with `"is_directory": true` are directories — feed their `path` back
through `--path` to go one level deeper, or skip the walking with `--recursive`.

```bash
sem-ai artifact list --scope jobs --id <job-id>
sem-ai artifact list --scope workflows --id <wf-id>

# Browse nested paths.
sem-ai artifact list --scope workflows --id <wf-id> --path test-results
sem-ai artifact list --scope workflows --id <wf-id> --recursive   # files only

sem-ai artifact get --scope jobs --id <job-id> --path <path> [--output file]

# Bulk download. Mirrors the remote tree under --output-dir (default
# ./artifacts-<id>, never the working directory), skips files already there
# unless --force, and --dry-run lists without writing.
sem-ai artifact pull --scope workflows --id <wf-id> --output-dir ./artifacts
sem-ai artifact pull --scope workflows --id <wf-id> --path test-results -o ./tr
```

`list --recursive`, `pull` and `pull --dry-run` share one rule: if the walk
could not see the whole tree — a directory failed to list, or it stopped at
the 500-listing cap (`SEM_AI_MAX_ARTIFACT_LISTINGS` raises it) — the command
reports what it found on stdout and then exits **non-zero** and sets
`"complete": false` in the payload. Key off `complete` — it is the one signal
all three share; `status` additionally distinguishes `pulled_partial` from
`dry_run_partial` for humans. Details come in `errors` and, for a capped walk,
`truncated` + `unvisited_directories`. The files it did find are still
written. Retrying does not recover missing directories; narrow with `--path`.

A 401, 403 or 429 is different: it is the organization refusing, not one bad
directory, so nothing more is attempted and the stderr `code` says why
(`rate_limited` with the advised wait, `feature_disabled`, or `api_error`).
Mid-walk, `list --recursive` and `pull --dry-run` still print what they found
with `"complete": false`, but a real `pull` prints nothing on stdout and
writes nothing. During the downloads, the files not yet started come back as
`"status": "not_attempted"` rows, counted in `not_attempted`, next to the ones
that did download. Wait it out or fix access before retrying; a failure
fetching one file from storage stays that file's own.

`get` needs a file path: pointed at a directory it fails with
`"code": "is_directory"` and names the two commands that do work on one.

## Pipeline YAML
```bash
sem-ai yaml validate --file .semaphore/semaphore.yml
```
