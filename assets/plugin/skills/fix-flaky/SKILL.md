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
histogram by default (rarely needed — `--full` restores it; no diagnosis path
below requires it). Pick a test that recurs across many commits and whose
`test_file` you can read.

### 2. Get the per-context history
```bash
sem-ai flaky show <test_id> --project <name>     # POSITIONAL test_id (NOT --file). Returns per-context pass_rate, p95, disruptions_count.
```
For the real failure, run `flaky failure <test_id>` (see *Pull the actual
failure*) — don't hand-chase run ids. (`latest_disruption_run_id` is on the
`flaky list` row, not `show`.) Contexts whose stats are all-null simply have no
disruptions recorded on that branch; ignore them and read the non-null ones.

### 3. Locate in the code (paths are app-relative)
`flaky failure` (step 2) already hands you the failing `file`+`line` — no need to
derive them from the test name. But the reported path is **app-relative**, not
repo-root: in a monorepo `test/foo/bar_test.exs` lives under an app/service dir
(e.g. `apps/api/test/…`, `apps/web/test/…`, `services/worker/test/…`). Resolve
the on-disk path:
```bash
git -C <repo> ls-files | grep -F "$(echo <test_file> | sed 's/:[0-9]*$//')"
```
If that returns matches in several apps, disambiguate with the `test_group`/suite
from `flaky show` (e.g. a group like `MyApp.Web.WidgetTest` → the `web` app).
Read the test AND the code it exercises — flakes live in the seam.

### 4. Diagnose — match the playbook
First **pull the real failure** when you can (see *Pull the actual failure* below):
the `left:`/`right:` + stacktrace beat guessing. Then match the playbook:
| signal | likely class | typical fix |
|---|---|---|
| asserts `==:lt`/`>` on consecutive `now()`/timestamps; ~95% pass | clock-tie / nondeterministic time | inclusive comparison on BOTH bounds (`in [:lt,:eq]` / `[:gt,:eq]`), or freeze/inject time |
| UI test clicks an element a poller/JS re-renders; `StaleReferenceError` | stale-element after async render | wrap the click in a retry-on-stale helper (a presence-assert does NOT fix it — the node goes stale *after* lookup) |
| in-test wait/sleep budget < failure tail | timeout too short for async work | raise the wait budget to match a non-flaky sibling; make the predicate nil-safe |
| asserts order of a list query with no `ORDER BY` | nondeterministic DB/collection order | add deterministic ordering at the source |
| depends on leftover state between tests | shared/global state | isolate setup/teardown; unique fixtures |
| asserts a count of OTP processes/children (e.g. `count_children` → `%{active: N, workers: N}`) that's off by one+ | leaked process from a prior test (shared named supervisor / registered GenServer) | terminate/drain the named processes in `setup`/`on_exit`, not just the DB |
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
or a targeted rerun, and check the pass rate moved.
**Can't verify** (no local toolchain, can't push, or testbox unavailable — e.g.
an org that blocks debug sessions)? Say so and mark the fix **provisional** —
that's an acceptable outcome, not a failure.

## Pull the actual failure (`flaky failure`)
```bash
sem-ai flaky failure <test_id> --project <name>
```
One call resolves the latest disruption's job, fetches its log, and returns the
failing test's **real assertion** as JSON: `{test_name, run_id, framework,
summary, matched, failures:[{file, line, message}]}`. `message` is the actual
`code:`/`left:`/`right:`/`stacktrace` — not a guess. It works for ExUnit (which
`test report` can't parse), and filters to your test. Pin a specific occurrence
with `--run-id <job_id>`.
- `matched:false` → the failure block didn't match your test name (the job ran it
  but it may have passed that run, or the name differs); it returns all failures
  in that job — eyeball them.
- `log_unavailable` → the disruption's job log aged out (retention); diagnose from
  source + the playbook above.
- Timeout-class flakes show a raised exception (e.g. `Timeout: ...`), not an
  assertion diff — `message` carries the exception, not a failing `assert`.
- For ExUnit, `message` often includes the full process **Logger output** after the
  assertion — read all of it; the event ordering there is frequently the decisive
  evidence (e.g. an async consumer firing *after* the step you tested), not just `left/right`.

**Manual fallback** (older binaries without `flaky failure`): a `run_id` from
`flaky disruptions <test_id>` (`.run_id`; skip null-padding rows) is a **job id** →
`job log <run_id>` (takes NO `--project`) → grep the failure block:
```bash
sem-ai job log <run_id> | jq -r '.[].output // empty' \
  | grep -nE '[0-9]+\) (test|doctest)|match \(=\) failed|left:|right:|stacktrace:'
```

## Composes with
- **test-intelligence** — `sem-ai test report|summary` failure detail (when retrievable).
- **debug-pipeline** — `sem-ai diagnose <run-id>` for the broader failed run.
- **testbox** — step 6 verification.

## Gotchas
- `flaky show`/`disruptions`/`failure` take the `test_id` **positionally** (the `args` field via MCP); `--file` returns empty silently.
- `flaky disruptions` can return null-timestamp padding rows — ignore them.
- `flaky show` per-context `pass_rate`/`disruptions_count` can be `null` even when disruptions exist — trust the disruption rows.
- Don't `sem-ai context switch` mid-task if one is set; pass `--project`.
