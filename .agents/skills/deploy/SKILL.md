---
name: deploy
description: Deploy via Semaphore promotions. Manage deployment targets, promote pipelines, deploy-and-wait.
user-invocable: false
---

# Deployments and Promotions

## See available promotions
```bash
sem-ai pipeline promote <pipeline-id>
# Returns list of available promotion targets (no --target = list mode)
```

## Deploy (promote)

```bash
# Dry run (safe — does NOT execute)
sem-ai pipeline promote <id> --target "Staging Deploy"

# Execute
sem-ai pipeline promote <id> --target "Staging Deploy" --confirm

# Execute and wait for promoted pipeline to finish
sem-ai promote-and-wait <id> --target "Staging Deploy" --confirm

# Override conditions (deploy despite failures)
sem-ai pipeline promote <id> --target "Staging" --confirm --override

# With parameters
sem-ai pipeline promote <id> --target "Production" --confirm --param version=1.2.3
```

## Deployment targets
```bash
sem-ai deploy targets --project my-app    # list
sem-ai deploy show <target-id>            # details
sem-ai deploy history <target-id>         # history
sem-ai deploy activate <target-id>        # enable
sem-ai deploy deactivate <target-id>      # disable
sem-ai deploy delete <target-id>          # remove
```

## Full deploy workflow
```bash
# 1. Verify tests pass
sem-ai test summary --pipeline <id>

# 2. Deploy to staging and wait
sem-ai promote-and-wait <id> --target "Staging" --confirm

# 3. Check staging result
# (promoted pipeline ID is in the output above)

# 4. Deploy to production
sem-ai promote-and-wait <id> --target "Production" --confirm
```

## Safety
- Without `--confirm`: dry run only.
- Always verify tests before promoting.
- `--override` bypasses conditions — confirm with user first.
