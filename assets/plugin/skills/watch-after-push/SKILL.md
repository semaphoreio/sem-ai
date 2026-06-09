---
name: watch-after-push
description: After pushing commits to a Semaphore-backed repo, find the triggered CI run and watch it to completion, then report pass/fail. Use right after `git push`, or when the user asks "did my push pass", "watch CI", "is the build green", "what happened after I pushed", "watch the pipeline".
user-invocable: false
allowed-tools: Bash(sem-ai *), Bash(git *)
---

# Watch CI after a push

When commits land on the remote of a Semaphore-managed repo (`.semaphore/` present), a pipeline is triggered. This skill finds that run for the exact commit you pushed and watches it to completion.

> CI triggers on **push**, not on a local commit. A local commit alone starts nothing — push first.

## Flow

```bash
# 0. Make sure the work is on the remote (CI fires on push, not commit).
git push

# 1. Pin the exact commit + branch you just pushed.
SHA=$(git rev-parse HEAD)
BRANCH=$(git rev-parse --abbrev-ref HEAD)

# 2. Find the workflow for THIS commit.
#    A webhook -> pipeline lag of a few seconds is normal, so poll until it appears.
#    Match on commit_sha (not just branch): collision-proof when several Semaphore
#    projects share one repo URL, and it avoids grabbing a stale earlier run.
WF=""
for _ in 1 2 3 4 5 6; do
  WF=$(sem-ai workflow list --branch "$BRANCH" 2>/dev/null \
        | jq -r --arg sha "$SHA" '.[] | select(.commit_sha==$sha) | .id' \
        | head -n1)
  [ -n "$WF" ] && break
  sleep 5
done

# 3. Watch to completion (polls every pipeline in the workflow until all are DONE).
if [ -n "$WF" ]; then
  sem-ai watch "$WF"
else
  echo "No workflow for $SHA yet — re-check 'sem-ai status' or the project mapping."
fi
```

## Notes

- `sem-ai workflow list` and `sem-ai status` auto-detect the project from the `origin` remote. Pass `--project <name>` only to override, or when the repo maps to several projects.
- **Shortcut:** `sem-ai status` already pins the current HEAD commit and returns `.workflow_id` — you can skip the loop above with `WF=$(sem-ai status --format json | jq -r .workflow_id)` once the run exists, or just poll `until sem-ai status --exit-code; do sleep 20; done` (0=pass, 8=pending, 1=fail).
- **Repo-URL collisions:** if more than one Semaphore project points at the same git remote, detection returns *all* of them — `sem-ai status` reports `"multiple_projects": true` instead of guessing. The `commit_sha` filter above keeps the loop on the right run; if more than one project ran the same commit, pin the one you mean with `--project`.
- `sem-ai watch` exits once every pipeline in the workflow reaches a terminal state, and reflects pass/fail in its output. Default poll interval 30s (min 5s, `--interval`).
- To diagnose *why* a watched run failed, hand off to the `debug-pipeline` skill (`sem-ai diagnose <workflow-id>`).
