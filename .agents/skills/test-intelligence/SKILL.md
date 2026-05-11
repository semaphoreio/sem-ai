---
name: test-intelligence
description: Analyze test results, detect flaky tests, parse JUnit artifacts. Supports Go, pytest, rspec, minitest, jest, ExUnit.
user-invocable: false
---

# Test Intelligence

## Quick summary
```bash
sem-agent test summary --pipeline <id>
```
Returns: verdict, total/passed/failed/skipped, failures with test name + file:line + message.

## Detailed per-job report
```bash
sem-agent test report --pipeline <id>
```
Each job shows: framework detected, source (artifact or log), counts, failed test details.

**Strategy:** tries artifact JUnit JSON first (richer data), falls back to log parsing.

**Frameworks:** Go/gotestsum, pytest, rspec, minitest, jest, ExUnit, JUnit JSON.

## Flaky detection
```bash
sem-agent test flaky --project my-app --branch main --count 10
```
Analyzes last N workflows. Flaky = test that sometimes passes, sometimes fails.

## Common workflows

**"Did tests pass?"**
```bash
sem-agent test summary --pipeline <id>   # check "verdict" field
```

**"Which test broke?"**
```bash
sem-agent test summary --pipeline <id>   # check "failures" array
```

**"Is this flaky?"**
```bash
sem-agent test flaky --project <p> --count 10
# Cross-reference failing test name with flaky_tests list
```

**"Get raw test artifact"**
```bash
sem-agent artifact get --scope jobs --id <job-id> --path test-results/junit.json --output results.json
```

**"Show raw test output"**
```bash
sem-agent job log <job-id> --format table
```
