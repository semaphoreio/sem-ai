---
name: debug-pipeline
description: Diagnose and fix CI pipeline failures. Step-by-step debugging with sem-ai.
user-invocable: false
---

# Debugging CI Failures

## Fast path — one command

```bash
sem-ai diagnose <workflow-id>
# or auto-detect from git:
sem-ai diagnose
sem-ai diagnose --project my-app --branch main
```

Returns: pipeline result, failed blocks, failed jobs with log tails AND parsed test results (file:line:message).

## Step-by-step

### 1. Find the workflow
```bash
sem-ai workflow list --project my-app --branch feature-x
```

### 2. See pipeline structure
```bash
sem-ai pipeline show <pipeline-id>
# Shows blocks and jobs. Look for "result": "failed"
```

### 3. Read logs
```bash
sem-ai job log <job-id>              # structured JSON
sem-ai job log <job-id> --format table  # human-readable
```

### 4. Get parsed test results
```bash
sem-ai test summary --pipeline <id>
```

Example output:
```json
{
  "verdict": "failed",
  "total": 11, "passed": 10, "failed": 1,
  "failures": [{"job": "go test", "test": "Test_timeHandler_statusCode", "file": "main_test.go", "line": 243, "message": "expected status 201, got 200"}]
}
```

### 5. Server-side diagnostics
```bash
sem-ai troubleshoot workflow <id>
sem-ai troubleshoot pipeline <id>
sem-ai troubleshoot job <id>
```

### 6. Check if flaky
```bash
sem-ai test flaky --project my-app --count 10
```

## After fixing

```bash
sem-ai workflow rerun <id>           # full rerun
sem-ai rerun-failed <pipeline-id>    # rebuild failed blocks only
sem-ai watch <new-workflow-id>       # wait for completion
sem-ai test summary --pipeline <id>  # verify
```

## Common patterns

| Log pattern | Cause | Next step |
|------------|-------|-----------|
| `exit_code: 1` on test command | Test failure | `sem-ai test summary --pipeline <id>` |
| Pipeline stuck `initializing` | YAML error | `sem-ai yaml validate --file .semaphore/semaphore.yml` |
| `result_reason: "stuck"` | No agent available | `sem-ai agent types` |
| All blocks empty | Compile failed | `sem-ai troubleshoot pipeline <id>` |
| `cache` errors | Cache not configured | Environment issue, not code |
