---
name: testbox
description: Run CI commands against local changes in a real Semaphore environment. Use when the user wants to test code before pushing, run tests in CI env, or iterate fast on fixes.
when_to_use: >
  Trigger on: "run tests", "test before pushing", "try this in CI", "does this pass",
  "test my changes", "run the build", "check if tests pass", "spin up CI environment"
user-invocable: false
---

# Testbox — Run CI locally in a real Semaphore environment

Testbox creates a Semaphore CI job you can run commands against via SSH. Same machine type, OS image, secrets, and cache as your real pipelines. Zero new infrastructure.

## Workflow

### 1. Warm up (once per session)

```bash
sem-ai testbox warmup --project my-app
```

Returns a testbox ID + SSH info. The VM is ready when the command returns.

Options:
```bash
--machine f1-standard-4       # bigger machine (default: f1-standard-2)
--os-image ubuntu2404          # different OS (default: ubuntu2204)
--duration 45m                 # longer session (default: 30m)
--idle-timeout 10m             # stop if no commands for N minutes (default: 30m)
```

### 2. Run commands (fast — rsync + SSH)

```bash
sem-ai testbox run --id <testbox-id> "go test ./..."
sem-ai testbox run --id <testbox-id> "make build"
sem-ai testbox run --id <testbox-id> "npm test"
```

Each `run` syncs only changed files (rsync checksum), then executes. After first sync, subsequent runs take 1-3 seconds for the sync.

### 3. Interactive SSH (optional)

```bash
sem-ai testbox ssh --id <testbox-id>
```

### 4. Stop when done

```bash
sem-ai testbox stop --id <testbox-id>
```

Or let it auto-expire after the duration/idle timeout.

## Best practices for agents

1. **Warm up immediately** when starting a coding task that involves CI. Don't wait until tests need to run.
2. **Reuse the testbox ID** across multiple `run` commands. Don't create a new testbox per test run.
3. **Route tests through testbox**, not locally. The CI environment has the correct dependencies, services, and secrets.
4. **After tests pass in testbox**, push the code. CI will confirm via the real pipeline.
5. **Stop the testbox** when the task is complete to avoid unnecessary billing.

## Typical agent loop

```bash
# Start of task
TESTBOX=$(sem-ai testbox warmup --project my-app | jq -r '.testbox_id')

# Iterate on code
# ... make changes ...
sem-ai testbox run --id $TESTBOX "go test ./..."
# ... fix failures ...
sem-ai testbox run --id $TESTBOX "go test ./..."
# tests pass!

# Push and verify in real CI
git push
sem-ai workflow list --project my-app --branch $(git branch --show-current)
sem-ai watch <workflow-id>

# Clean up
sem-ai testbox stop --id $TESTBOX
```
