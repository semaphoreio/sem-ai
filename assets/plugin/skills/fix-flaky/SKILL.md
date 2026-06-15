---
name: fix-flaky
description: Triage and FIX flaky tests on a Semaphore project end-to-end — find the worst offenders, pull history, locate the test in the code, diagnose the root cause, write a fix or justified quarantine, and verify by re-running. Use whenever the user wants to fix/investigate flaky tests, de-flake CI, reduce intermittent failures, asks "why is CI randomly red", wants to quarantine a flake, or after `sem-ai flaky list` surfaces offenders. Goes BEYOND detection (test-intelligence) to root-cause + code change + verification.
user-invocable: true
---

# Fix Flaky Tests

Detection tells you a test is flaky; this skill closes the loop to a fix. The
hard part is never "is it flaky" — it's tying the failure to the actual code.

## The loop

### 1. Discover + rank (ranked, denoised, compact — no jq needed)
```bash
sem-ai flaky list --project <name> \
  --sort-field total_disruptions_count --sort-dir desc \
  --disruptions ">1"
```
`--disruptions ">1"` drops one-off noise (single-failure `pass_rate:50` rows);
the sort ranks by recurrence. Output omits the per-test `disruption_history`
histogram by default (pass `--full` if you ever need it). Pick a test that
recurs across many commits and whose `test_file` you can read.

### 2. Get the per-context history
```bash
sem-ai flaky show <test_id> --project <name>     # POSITIONAL test_id (NOT --file). per-context pass_rate, p95, latest_disruption_run_id
```

### 3. Locate in the code (paths are app-relative)
`test_file` is relative to the *app* root, not the repo root; in a monorepo
`test/foo/bar_test.exs` lives at e.g. `plumber/ppl/test/foo/bar_test.exs`:
```bash
git -C <repo> ls-files | grep -F "$(echo <test_file> | sed 's/:.*//')"
```
Read the test AND the code it exercises — flakes live in the seam.

### 4. Diagnose — match the playbook
| signal | likely class | typical fix |
|---|---|---|
| asserts `==:lt`/`>` on consecutive `now()`/timestamps; ~95% pass | clock-tie / nondeterministic time | inclusive comparison on BOTH bounds (`in [:lt,:eq]` / `[:gt,:eq]`), or freeze/inject time |
| UI test clicks an element a poller/JS re-renders; `StaleReferenceError` | stale-element after async render | retry-on-stale click; assert element present first |
| in-test wait/sleep budget < failure tail | timeout too short for async work | raise the wait budget to match a non-flaky sibling; make the predicate nil-safe |
| asserts order of a list query with no `ORDER BY` | nondeterministic DB/collection order | add deterministic ordering at the source |
| depends on leftover state between tests | shared/global state | isolate setup/teardown; unique fixtures |
| calls a real external service | external dependency | stub/mock, or mark + isolate |

p95 (from `flaky show`) is the heuristic **only for the timeout row** — for
clock-tie/stale-element/ordering it's a red herring. For the timeout class,
compare the wait budget to the failure **tail**, not p95 (a ~95%-pass flake's
p95 sits under the budget); the real ceiling is **wait-helper fan-out ×
per-wait budget**. Two high-value moves: `grep` the repo for other callers of
that wait helper and **diff their budgets** (a non-flaky sibling is proof +
fix template); and before writing any retry/wait machinery, `grep` for an
existing helper (`retry_on_stale`, `assert_eventually`, a shared `Wait` util)
and **reuse it**.

### 5. Fix or quarantine
Smallest change that removes the nondeterminism. A justified quarantine
(skip/tag + linked ticket) is acceptable if a true fix is out of scope — say why.
Match repo conventions; no comments unless the repo uses them.

### 6. Verify by RE-RUNNING (one green proves nothing)
Use the **testbox** skill to run the single test many times against your change,
or a targeted rerun, and check the pass rate moved. If you can't verify (no
local toolchain, can't push, testbox unavailable — e.g. an org that blocks
debug sessions), say so and mark the fix **provisional**.

## Getting the actual failure (best-effort)
Bridging a disruption to the real assertion text is unreliable by design:
`latest_disruption_run_id` is a *workflow* id (`sem-ai test report` wants a
*pipeline* id), `test report` doesn't parse ExUnit (most of this monorepo), and
old runs are past retention. For ExUnit/old flakes, diagnose from source + the
failure-name playbook above — the test name + history + code are usually enough.

## Composes with
- **test-intelligence** — `sem-ai test report|summary` failure detail (when retrievable).
- **debug-pipeline** — `sem-ai diagnose <run-id>` for the broader failed run.
- **testbox** — step 6 verification.

## Gotchas
- `flaky show`/`disruptions` take the `test_id` **positionally**; `--file` returns empty silently.
- `flaky disruptions` can return null-timestamp padding rows — ignore them.
- `flaky show` per-context `pass_rate`/`disruptions_count` can be `null` even when disruptions exist — trust the disruption rows.
- Don't `sem-ai context switch` mid-task if one is set; pass `--project`.
