---
name: fix-flaky
description: Triage and FIX flaky tests on a Semaphore project end-to-end — discover the worst offenders, pull the ACTUAL failure (not just that it's flaky), find the test in the code, diagnose the root cause, write a fix or justified quarantine, and verify by re-running. Use whenever the user wants to fix/investigate flaky tests, "de-flake" CI, reduce intermittent failures, asks "why is CI randomly red", wants to quarantine a flake, or after `sem-ai flaky list` surfaces offenders. This goes BEYOND detection (see test-intelligence) to root-cause + code change + verification. Prefer this over reading logs by hand when a test passes sometimes and fails sometimes.
user-invocable: true
---

# Fix Flaky Tests

Detection tells you a test is flaky; this skill closes the loop to a fix. The
hard part is never "is it flaky" — it's getting the *actual failure* and tying
it to the *actual code*. Most of the steps below exist to bridge those two gaps,
because the flaky data alone (pass rate, disruption count) doesn't tell you
*why*.

## Fast path

```bash
scripts/triage.sh list --project <name>            # ranked, compact, noise filtered
scripts/triage.sh inspect --project <name> --test-id <id> --repo <repo-checkout>
```
`inspect` does the three mechanical things every triage needs: pulls the test's
history, resolves its `test_file` to the real on-disk path (monorepo-aware), and
fetches the actual failure output from the last disruption. Then you diagnose +
fix. The manual steps below are the same flow if you need to deviate.

## The loop

### 1. Discover + rank (don't trust the default output)
```bash
sem-ai flaky list --project <name> --format json | jq -r '
  [ .[] | select((.disruptions_count // 0) > 1 and (.resolved | not)) ]
  | sort_by(-(.disruptions_count // 0))
  | .[] | "\(.disruptions_count)\t\(.pass_rate)%\t\(.test_file)\t\(.test_name)\t\(.test_id)"'
```
Why the jq: the dataset is full of one-off noise (`pass_rate: 50, disruptions_count: 1`
from a single failure) that buries the real, *recurring* flakes. Rank by
disruption count and drop the singletons. **Do not use `--format table`** — it
inlines `disruption_history` and prints one unreadable multi-KB line. JSON +
projection is the only sane view.

Pick a test you can confidently root-cause — recurring across many commits
(not one bad run), with a `test_file` you can read.

### 2. Get the ACTUAL failure (the step everyone skips)
The disruption data says *that* it failed, never *how*. Get the per-context
history and (when retrievable) the real failure:
```bash
sem-ai flaky show <test_id> --project <name>   # POSITIONAL test_id (NOT --file). Per-context pass_rate, p95, latest_disruption_run_id
sem-ai flaky disruptions <test_id> --project <name>   # individual runs; ignore rows with null timestamp
```
Note: `flaky show`/`disruptions` take the `test_id` **positionally** — `--file`
returns empty *silently* (exit 0), which reads as "no data." Get the `test_id`
from the `list` jq above.

Bridging to the actual failure text is unreliable in this repo, by design —
budget little time for it:
- `latest_disruption_run_id` is a **workflow** id; `sem-ai test report` wants a
  **pipeline** id, so it 404s unless you resolve workflow→pipeline first.
- `sem-ai test report` parses Go/pytest/rspec/jest — **not ExUnit**, which is
  most of this monorepo. So even a live run yields nothing for `.exs` tests.
- Old disruptions are past artifact retention (404) regardless.

So for ExUnit/old flakes, expect to diagnose from **source + the failure-name
pattern** (the playbook below maps names → classes). That's not a failure of
method — the test name + history + code are usually enough.

### 3. Locate in the code (paths are app-relative)
`test_file` from sem-ai is relative to the *app* root, not the repo root. In a
monorepo `test/foo/bar_test.exs` actually lives at e.g.
`plumber/ppl/test/foo/bar_test.exs` or `ee/rbac/test/...`. Resolve it:
```bash
find <repo> -path "*/$(echo <test_file> | sed 's#^test/#test/#')" 2>/dev/null   # or just: find <repo> -path "*$test_file"
```
Read the test AND the code it exercises (and any shared setup/helpers) — flakes
usually live in the seam between them.

### 4. Diagnose — match against the flake playbook
| signal | likely class | typical fix |
|---|---|---|
| asserts `==:lt`/`>` on consecutive `now()`/timestamps; `pass_rate` ~95% | clock-tie / nondeterministic time | inclusive comparison (`in [:lt,:eq]`), or freeze/inject time |
| UI test clicks an element a poller/JS re-renders; `StaleReferenceError` | stale-element after async render | retry-on-stale click, assert element present first |
| in-test wait/sleep budget < observed `p95` (from `flaky show`) | timeout too short for async work | raise the wait budget (match a non-flaky sibling), make predicate nil-safe |
| asserts order of a list query with no `ORDER BY` | nondeterministic DB/collection order | add deterministic ordering at the source |
| depends on leftover state between tests | shared/global state | isolate setup/teardown, unique fixtures |
| calls a real external service / network | external dependency | stub/mock, or mark + isolate |

For the timeout class, compare the in-test wait/sleep budget to the failure
**tail, not p95** — a ~95%-pass flake has p95 *under* the budget by definition;
it dies in the p99/max. The sharper tell: count how many times the test funnels
through a shared wait helper — the real ceiling is **fan-out × per-wait budget**
(e.g. a test with 3 sequential async waits on a 1s helper has a ~3s effective
budget against a tail that exceeds it). Also scan sibling tests in the same file:
if flakiness tracks the number of waits/interactions, that gradient is your proof.
When unsure, prefer a contained fix and lean on existing repo helpers
(retry/assert-eventually wrappers, shared wait utilities) over new machinery.

### 5. Fix or quarantine
Implement the smallest change that removes the nondeterminism. If a real fix is
genuinely out of scope, a *justified* quarantine (skip/tag with a linked ticket)
is acceptable — say why. Match the repo's conventions; don't add comments unless
the repo does.

### 6. Verify by RE-RUNNING (one green proves nothing)
A flake needs repeated runs to confirm. Use the **testbox** skill to run the
single test many times against your change, or trigger a targeted rerun, and
check the pass rate moved. Don't declare it fixed off a single pass.

If you can't verify (no local toolchain; can't push; testbox unavailable),
say so and mark the fix **provisional** — a reasoned by-construction fix is fine
to hand over, but don't claim it's confirmed.

## Composes with
- **test-intelligence** — step 2's failure detail (`sem-ai test report|summary`).
- **debug-pipeline** — `sem-ai diagnose <run-id>` for the broader failed run.
- **semaphore-test-results** — where the JUnit detail comes from; per-framework setup.
- **testbox** — step 6 verification.

## Gotchas (learned the hard way)
- `--file` is exact-match (incl. `:line`); substrings/prefixes silently return `[]`. Filter client-side with jq instead.
- `sem-ai flaky disruptions` can return empty/padded rows before the real data — drop rows with null `timestamp`.
- `flaky show` per-context `pass_rate`/`disruptions_count` can be `null` even when `disruptions` proves failures — trust the disruption rows over a null aggregate.
- Don't `sem-ai context switch` mid-task if one is already set; pass `--project` explicitly.
