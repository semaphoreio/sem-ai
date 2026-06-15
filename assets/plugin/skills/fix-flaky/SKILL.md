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
The disruption data says *that* it failed, never *how*. Bridge to the real
assertion/stacktrace before theorizing:
```bash
sem-ai flaky show --project <name> --file <test_file>   # or by id; gives latest_disruption_run_id + per-context pass_rate/p95
sem-ai test report --pipeline <run-id>                  # test-intelligence: parsed failure (test name + file:line + message)
```
Use the `latest_disruption_run_id` (or a run id from `sem-ai flaky disruptions`)
to pull the failing run's test detail via the **test-intelligence** skill. The
failure message usually names the flake class outright (timeout, stale element,
comparison, ordering), which beats guessing from source.

Reality check: for OLD disruptions the run's artifacts/logs may be expired
(`test report` returns HTTP 404). If so, walk `sem-ai flaky disruptions` for the
most RECENT run id and try that; if every available run is past retention,
accept that you'll diagnose from source + the failure *name* pattern (the
playbook below maps names → classes). Don't burn time fighting a 404.

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

`flaky show`'s **p95 vs any in-test timeout** is the single best heuristic for
the timeout class — compare them explicitly. When unsure, prefer a contained
fix and lean on existing repo helpers (e.g. retry/assert-eventually wrappers,
shared wait utilities) over inventing new machinery.

### 5. Fix or quarantine
Implement the smallest change that removes the nondeterminism. If a real fix is
genuinely out of scope, a *justified* quarantine (skip/tag with a linked ticket)
is acceptable — say why. Match the repo's conventions; don't add comments unless
the repo does.

### 6. Verify by RE-RUNNING (one green proves nothing)
A flake needs repeated runs to confirm. Use the **testbox** skill to run the
single test many times against your change, or trigger a targeted rerun, and
check the pass rate moved. Don't declare it fixed off a single pass.

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
