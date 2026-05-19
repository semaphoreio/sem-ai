---
name: project-health
description: Monitor Semaphore project health — pass rates, recent failures, deployment status, trends.
user-invocable: false
---

# Project Health Monitoring

## Quick health check
```bash
sem-ai health --project my-app
```
Returns: verdict (healthy/degraded/unhealthy), pass rate, failed/passed/other counts, deploy target count.

## CI status for a branch
```bash
sem-ai status --project my-app --branch main
sem-ai status --project my-app --pr 422
sem-ai status   # auto-detects project + branch from git
```

## Recent workflows
```bash
sem-ai workflow list --project my-app
sem-ai workflow list --project my-app --branch main
```

## Pipeline details
```bash
sem-ai pipeline list --project my-app    # all recent pipelines
sem-ai pipeline show <id>                # blocks + jobs tree
```

## Test trends
```bash
sem-ai test flaky --project my-app --count 10   # flaky tests across last 10 workflows
sem-ai test summary --pipeline <id>              # test results for specific run
```

## Deployment status
```bash
sem-ai deploy targets --project my-app
sem-ai deploy history <target-id>
```

## Interpreting health verdict
- **healthy**: pass rate >= 80%
- **degraded**: pass rate 50-80%
- **unhealthy**: pass rate < 50%
