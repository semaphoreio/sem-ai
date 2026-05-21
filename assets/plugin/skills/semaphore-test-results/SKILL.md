---
name: semaphore-test-results
description: Publish JUnit test reports from Semaphore jobs and surface them in the UI's Test Reports tab. Covers the `test-results` CLI, why it must live in `epilogue` (not the test command itself), and per-framework JUnit configuration for Go, pytest, RSpec, Jest, Vitest, ExUnit, Java. Use when a pipeline writes JUnit / test reports, the Test Reports tab is empty after a failure, failures publish silently, the user asks about flaky test surfacing or per-framework JUnit setup, or any time `test-results publish` / `test-results gen-pipeline-report` is involved.
---

# Publishing test results on Semaphore

The `test-results` CLI (part of the toolbox, preinstalled) uploads JUnit-formatted reports to Semaphore's artifact store and renders them in the **Test Reports** tab of each job / pipeline. Without it, a failed test is just a red job — no per-test detail, no failure history, no flake detection.

This skill is the depth that `semaphore-toolbox` defers to. See `semaphore-toolbox` for the broader CLI surface.

## The two commands

```
test-results publish <junit-file> [--name <suite>] [--generate-mcp-summary]
test-results gen-pipeline-report [--generate-mcp-summary]
```

- **`publish`** — uploads ONE JUnit file as an artifact, scoped to the current job; the file appears under that job's Test Reports tab. Takes one file argument. `--name` labels the suite in the UI (useful when one job emits multiple junit files for different test types).
- **`gen-pipeline-report`** — runs once at end of pipeline, gathers every junit artifact the publishes uploaded, and produces the **pipeline-level aggregated report** (the rolled-up view across all jobs).

`--generate-mcp-summary` (on either command) also emits `mcp-summary.json` — a compact, AI-readable summary of pass/fail counts and failure messages.

## Why epilogue, not inline (THE rule)

Put `test-results publish` in the job's **epilogue**, never in the main `commands:` list. Here's why:

| Where you put it | What happens when tests fail |
|---|---|
| Inline (after test command) | Test command exits non-zero → shell stops → publish never runs → no report in UI |
| **`epilogue.always.commands`** | Runs even when the job fails — publish always fires → failure detail surfaces in UI |

The epilogue under `always:` is Semaphore's "finally" block. Without it, the only thing you ever see for a failing job is the raw stderr, which buries the actually useful information.

### Canonical pipeline shape

```yaml
global_job_config:
  prologue:
    commands:
      - checkout
  epilogue:
    always:
      commands:
        # Publish every junit-*.xml the job produced.
        # Loop pattern lets a single job emit multiple suite reports.
        - 'for f in junit-*.xml; do [ -f "$f" ] || continue; n="${f#junit-}"; n="${n%.xml}"; test-results publish "$f" --name "$n"; done'

blocks:
  - name: Test
    task:
      jobs:
        - name: ...
          commands:
            - ...
            # produces junit-unit.xml and junit-integration.xml
            - go test -junit unit.xml ./...
            - go test -junit integration.xml ./integration

after_pipeline:
  task:
    jobs:
      - name: Pipeline test report
        commands:
          - test-results gen-pipeline-report
```

The loop pattern is what we use in `sem-ai`'s own pipeline — it lets a job emit `junit-tests.xml`, `junit-race.xml`, etc., and each becomes its own labeled suite without changing the epilogue.

## Per-framework JUnit configuration

Each test framework needs a flag, plugin, or formatter to write JUnit XML. The publishing step is identical across frameworks; only the test invocation changes.

### Go — gotestsum

`go test` itself doesn't emit JUnit. Use `gotestsum` (small wrapper).

```yaml
- go install gotest.tools/gotestsum@latest
- gotestsum --junitfile junit-tests.xml --format pkgname -- ./...
```

For race detection in a separate job:
```yaml
- gotestsum --junitfile junit-race.xml --format pkgname -- -race ./...
```

### Python — pytest

Built-in:
```yaml
- pytest --junitxml=junit-pytest.xml
```

### Ruby — RSpec

Needs the `rspec_junit_formatter` gem in your bundle:
```ruby
# Gemfile
group :test do
  gem 'rspec_junit_formatter'
end
```

Then:
```yaml
- bundle exec rspec --format RspecJunitFormatter --out junit-rspec.xml
```

### Ruby — Minitest

Needs `minitest-junit`:
```ruby
gem 'minitest-junit'
```
```yaml
- ruby -Itest -r minitest-junit -e 'Dir["test/**/*_test.rb"].each { |f| require_relative f }' --junit --junit-filename=junit-minitest.xml
```

### JavaScript — Jest

Install `jest-junit`:
```bash
npm i -D jest-junit
```

Config (in `package.json` or `jest.config.js`):
```json
"jest": {
  "reporters": ["default", ["jest-junit", { "outputFile": "junit-jest.xml" }]]
}
```

### JavaScript — Vitest

Built-in JUnit reporter:
```yaml
- npx vitest run --reporter=junit --outputFile=junit-vitest.xml
```

### Elixir — ExUnit

Add `junit_formatter`:
```elixir
# mix.exs
{:junit_formatter, "~> 3.0", only: [:test]}
```
```elixir
# config/test.exs
config :junit_formatter,
  report_dir: ".",
  report_file: "junit-exunit.xml"
```
```yaml
- mix test
```

### Java — Maven

Maven Surefire produces XML by default at `target/surefire-reports/TEST-*.xml`. Move/rename so the epilogue loop picks them up:
```yaml
- mvn test
- 'for f in target/surefire-reports/TEST-*.xml; do mv "$f" "junit-$(basename "$f" .xml | sed s/^TEST-//).xml"; done'
```

### Cypress (E2E)

`cypress-junit-reporter`:
```bash
npm i -D cypress-multi-reporters cypress-junit-reporter
```
```js
// cypress.config.js
reporter: 'cypress-multi-reporters',
reporterOptions: {
  reporterEnabled: 'cypress-junit-reporter',
  cypressJunitReporterReporterOptions: {
    mochaFile: 'junit-cypress-[hash].xml'
  }
}
```

When sharding Cypress across N jobs (see `semaphore-blocks` parallelism), each shard writes its own file — the epilogue loop publishes them with shard-distinguished names.

## What shows up in the UI

- **Per-job Test Reports tab** — populated by `test-results publish` inside that job. Lists each test with status, duration, error message; failed tests sorted first.
- **Pipeline-level Test Report** — populated by `test-results gen-pipeline-report` in `after_pipeline`. Aggregates every junit artifact across every job. This is what shows you "8 failed across 4 jobs" at a glance.
- **`mcp-summary.json` artifact** — when `--generate-mcp-summary` is passed; compact JSON suitable for downstream tooling (CI annotations, AI summarization).

If you skip `gen-pipeline-report`, the per-job tabs still work, but the rolled-up pipeline view is empty.

## Common failure modes

### "I see no test report, but the job's command produced junit-foo.xml"

Almost always: publish wasn't in the epilogue, was inline, and the test command exited non-zero before the publish line ran. Move publish to `epilogue.always.commands`.

### "Report is partial — only some suites show up"

Either:
- The epilogue loop missed a file (glob doesn't match — check the actual filename produced by the framework)
- A `test-results publish` call returned non-zero and `set -e` killed subsequent publishes. Wrap each call: `test-results publish "$f" --name "$n" || true` if you want best-effort. Usually better to fix the offending file.

### "Test Reports tab is empty after a passing job"

Pass-only state: junit file wasn't written (config issue) or path mismatch (file is in a subdir the epilogue glob doesn't reach). Confirm the framework actually emits the file and where.

### "Pipeline-level report is empty even though per-job reports work"

`gen-pipeline-report` not running. Check:
- It's in `after_pipeline.task.jobs[].commands`, not inside a block
- The job in `after_pipeline` actually runs (no missing prologue dep)

## Naming convention recommendation

Adopt `junit-<suite>.xml` everywhere (the loop pattern depends on it). Examples:
- `junit-tests.xml` (default Go unit tests)
- `junit-race.xml` (race detection)
- `junit-trivy.xml` (security scan as test results)
- `junit-cypress-1.xml`, `junit-cypress-2.xml`, … (sharded Cypress)
- `junit-vitest.xml`

The `--name` arg in the loop strips `junit-` and `.xml`, so the suite labels in the UI become `tests`, `race`, `cypress-1`, etc.

## Related skills

- `semaphore-toolbox` — broader CLI surface (cache, artifact, retry, sem-version, sem-service); test-results is the deep dive
- `semaphore-blocks` — where epilogue lives; how block / global_job_config / after_pipeline relate
- `test-intelligence` — analyzing the results *after* publication (flaky detection, trends); complements this skill
- `gha-to-semaphore` — when translating GHA test-reporter actions, point here
