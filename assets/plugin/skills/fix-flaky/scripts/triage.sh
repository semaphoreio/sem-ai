#!/usr/bin/env bash
# fix-flaky triage helper. Does the mechanical parts of flaky triage that are
# the same every time, so the agent can spend its tokens on diagnosis + the fix:
#   list     - ranked, noise-filtered, compact flaky list (the default output is unusable)
#   inspect  - for one test: history + resolve test_file -> real on-disk path + the actual failure
#
# Requires: sem-ai (context already set), jq. Pass --project explicitly.
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage:
  triage.sh list    --project <name> [--area <regex>] [--min-disruptions N]
  triage.sh inspect --project <name> --test-id <id> [--repo <path>]

list    -> recurring flakes only (disruptions > min, unresolved), ranked, as columns.
inspect -> per-test history + on-disk path (monorepo-aware) + the failing run's test detail.
EOF
  exit 2
}

[[ $# -ge 1 ]] || usage
CMD="$1"; shift
PROJECT=""; AREA=""; MIN=2; TEST_ID=""; REPO="."
while [[ $# -gt 0 ]]; do
  case "$1" in
    --project) PROJECT="$2"; shift 2;;
    --area) AREA="$2"; shift 2;;
    --min-disruptions) MIN="$2"; shift 2;;
    --test-id) TEST_ID="$2"; shift 2;;
    --repo) REPO="$2"; shift 2;;
    *) echo "unknown arg: $1" >&2; usage;;
  esac
done
[[ -n "$PROJECT" ]] || { echo "--project is required" >&2; usage; }
command -v jq >/dev/null || { echo "jq required" >&2; exit 3; }

flaky_json() { sem-ai flaky list --project "$PROJECT" --format json 2>/dev/null; }

case "$CMD" in
  list)
    # Rank recurring flakes; drop one-off noise (single-failure pass_rate:50 rows).
    # --area is an optional regex over test_file (client-side; the CLI --file is exact-match).
    flaky_json | jq -r --argjson min "$MIN" --arg area "$AREA" '
      [ .[]
        | select((.disruptions_count // 0) >= $min and (.resolved | not))
        | select($area == "" or (.test_file // "" | test($area))) ]
      | sort_by(-(.disruptions_count // 0))
      | (["DISRUPT","PASS%","FILE","TEST","ID"], ["-------","-----","----","----","--"]),
        (.[] | [ (.disruptions_count|tostring), ((.pass_rate|tostring)+"%"), .test_file, .test_name, .test_id ])
      | @tsv' | column -t -s$'\t'
    ;;
  inspect)
    [[ -n "$TEST_ID" ]] || { echo "--test-id is required for inspect" >&2; usage; }
    REC="$(flaky_json | jq -c --arg id "$TEST_ID" '.[] | select(.test_id == $id)')"
    [[ -n "$REC" ]] || { echo "no flaky test with id $TEST_ID in project $PROJECT" >&2; exit 4; }
    TEST_FILE="$(jq -r '.test_file' <<<"$REC")"
    TEST_FILE_NOLINE="${TEST_FILE%%:*}"   # strip the :NNN line suffix sem-ai appends
    RUN_ID="$(jq -r '.latest_disruption_run_id // empty' <<<"$REC")"

    echo "### test"
    jq -r '"  name: \(.test_name)\n  file: \(.test_file)\n  pass_rate: \(.pass_rate)%   disruptions: \(.disruptions_count)"' <<<"$REC"

    echo "### history (per-context pass_rate + p95 — the timeout-class heuristic)"
    # p95 lives in `flaky show <test_id>`, NOT the list record. Dump every p95/pass_rate scalar robustly.
    sem-ai flaky show "$TEST_ID" --project "$PROJECT" 2>/dev/null \
      | jq -r '[paths(scalars) as $p | select(($p[-1]|type)=="string" and ($p[-1]|test("p95|pass_rate|disrupt";"i"))) | "  \($p|join(".")) = \(getpath($p))"] | (if length>0 then .[] else "  (no per-context detail)" end)' \
      || echo "  (flaky show returned nothing — check: sem-ai flaky show $TEST_ID --project $PROJECT)"

    echo "### on-disk path (sem-ai paths are app-relative; resolving against $REPO)"
    if git -C "$REPO" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
      HITS="$(git -C "$REPO" ls-files | grep -F "/$TEST_FILE_NOLINE" || git -C "$REPO" ls-files | grep -F "$TEST_FILE_NOLINE" || true)"
      [[ -n "$HITS" ]] && HITS="$(echo "$HITS" | sed "s#^#$REPO/#")"
    else
      HITS="$(find "$REPO" -path "*/$TEST_FILE_NOLINE" 2>/dev/null | grep -v '/.git/' || true)"
    fi
    if [[ -n "$HITS" ]]; then echo "$HITS" | sed 's/^/  /'; else echo "  (not found — try the basename)"; fi

    echo "### actual failure (best-effort — usually NOT retrievable here, see notes)"
    if [[ -n "$RUN_ID" ]]; then
      OUT="$(sem-ai test report --pipeline "$RUN_ID" 2>&1)"; RC=$?
      if [[ $RC -eq 0 && -n "$OUT" && "$OUT" != *'"error"'* ]]; then echo "$OUT"; else
        echo "  could not fetch failure for run $RUN_ID:"
        echo "$OUT" | sed 's/^/    /' | head -4
        echo "  NOTE: latest_disruption_run_id is a WORKFLOW id; test report wants a PIPELINE id."
        echo "        test report also doesn't parse ExUnit (.exs). For ExUnit/old flakes, diagnose from source + the failure-name playbook."
      fi
    else
      echo "  (no latest_disruption_run_id; try: sem-ai flaky disruptions $TEST_ID --project $PROJECT)"
    fi
    ;;
  *) usage;;
esac
