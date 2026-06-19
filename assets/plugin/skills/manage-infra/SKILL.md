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
sem-ai task show <id>
sem-ai task run <id>                # trigger now
sem-ai task delete <id>

# Parameterized tasks: pass parameters, branch, and pipeline file.
# Parameters go to the task's run_now as a map; branch/pipeline-file pick
# the ref + YAML the task pipeline runs.
sem-ai task run <id> --param KEY=VALUE [--param ...] \
  [--branch <ref>] [--pipeline-file <path>]
```

## Artifacts
```bash
sem-ai artifact list --scope jobs --id <job-id>
sem-ai artifact list --scope workflows --id <wf-id>
sem-ai artifact get --scope jobs --id <job-id> --path <path> [--output file]
```

## Pipeline YAML
```bash
sem-ai yaml validate --file .semaphore/semaphore.yml
```
