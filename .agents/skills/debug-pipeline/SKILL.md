---
name: debug-pipeline
description: Diagnose and fix CI pipeline failures. Step-by-step debugging with sem-agent.
user-invocable: false
---

# Debugging CI Failures

## Fast path — one command

```bash
sem-agent diagnose <workflow-id>
# or auto-detect from git:
sem-agent diagnose
sem-agent diagnose --project my-app --branch main
```

Returns: pipeline result, failed blocks, failed jobs with log tails AND parsed test results (file:line:message).

## Step-by-step

### 1. Find the workflow
```bash
sem-agent workflow list --project my-app --branch feature-x
```

### 2. See pipeline structure
```bash
sem-agent pipeline show <pipeline-id>
# Shows blocks and jobs. Look for "result": "failed"
```

### 3. Read logs
```bash
sem-agent job log <job-id>              # structured JSON
sem-agent job log <job-id> --format table  # human-readable
```

### 4. Get parsed test results
```bash
sem-agent test summary --pipeline <id>
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
sem-agent troubleshoot workflow <id>
sem-agent troubleshoot pipeline <id>
sem-agent troubleshoot job <id>
```

### 6. Check if flaky
```bash
sem-agent test flaky --project my-app --count 10
```

## After fixing

```bash
sem-agent workflow rerun <id>           # full rerun
sem-agent rerun-failed <pipeline-id>    # rebuild failed blocks only
sem-agent watch <new-workflow-id>       # wait for completion
sem-agent test summary --pipeline <id>  # verify
```

## Common patterns

| Log pattern | Cause | Next step |
|------------|-------|-----------|
| `exit_code: 1` on test command | Test failure | `sem-agent test summary --pipeline <id>` |
| Pipeline stuck `initializing` | YAML error | `sem-agent yaml validate --file .semaphore/semaphore.yml` |
| `result_reason: "stuck"` | No agent available | `sem-agent agent types` |
| All blocks empty | Compile failed | `sem-agent troubleshoot pipeline <id>` |
| `cache` errors | Cache not configured | Environment issue, not code |
