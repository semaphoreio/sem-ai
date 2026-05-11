---
name: manage-infra
description: Manage Semaphore infrastructure — secrets, notifications, agent types, scheduled tasks, dashboards, artifacts.
user-invocable: false
---

# Infrastructure Management

## Secrets
```bash
sem-agent secret list [--project <p>]              # org or project level
sem-agent secret show <name> [--project <p>]
sem-agent secret create <name> --env KEY=VALUE [--project <p>]
sem-agent secret update <name> --env KEY=NEW [--project <p>]
sem-agent secret delete <name> [--project <p>]
```

## Notifications
```bash
sem-agent notification list
sem-agent notification show <name>
sem-agent notification delete <name>
```

## Self-hosted agents
```bash
sem-agent agent types                  # list agent types
sem-agent agent show <type-name>       # type details
sem-agent agent list --type <name>     # list agents of a type
sem-agent agent delete <type-name>     # delete type
```

## Scheduled tasks
```bash
sem-agent task list --project <p>
sem-agent task show <id>
sem-agent task run <id>                # trigger now
sem-agent task delete <id>
```

## Dashboards
```bash
sem-agent dashboard list
sem-agent dashboard show <name>
sem-agent dashboard delete <name>
```

## Artifacts
```bash
sem-agent artifact list --scope jobs --id <job-id>
sem-agent artifact list --scope workflows --id <wf-id>
sem-agent artifact get --scope jobs --id <job-id> --path <path> [--output file]
```

## Pipeline YAML
```bash
sem-agent yaml validate --file .semaphore/semaphore.yml
```
