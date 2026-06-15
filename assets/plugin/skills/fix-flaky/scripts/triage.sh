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
    jq -r '"  name: \(.test_name)\n  file: \(.test_file)\n  pass_rate: \(.pass_rate)%   disruptions: \(.disruptions_count)   p95: \(.p95_duration // "?")"' <<<"$REC"

    echo "### on-disk path (sem-ai paths are app-relative; resolving against $REPO)"
    HITS="$(find "$REPO" -path "*/$TEST_FILE_NOLINE" 2>/dev/null | grep -v '/.git/' || true)"
    [[ -z "$HITS" ]] && HITS="$(find "$REPO" -path "*$TEST_FILE_NOLINE" 2>/dev/null | grep -v '/.git/' || true)"
    if [[ -n "$HITS" ]]; then echo "$HITS" | sed 's/^/  /'; else echo "  (not found — try a looser pattern on the basename)"; fi

    echo "### actual failure (from last disruption run $RUN_ID)"
    if [[ -n "$RUN_ID" ]]; then
      # Best-effort bridge: the run id may be a pipeline or workflow id. Try the
      # test-intelligence report; if that surface doesn't match, fall back to diagnose.
      sem-ai test report --pipeline "$RUN_ID" 2>/dev/null \
        || sem-ai diagnose "$RUN_ID" 2>/dev/null \
        || echo "  (could not auto-fetch; run: sem-ai test report --pipeline $RUN_ID  /  sem-ai job log <job-id>)"
    else
      echo "  (no latest_disruption_run_id on this record; use: sem-ai flaky disruptions --project $PROJECT --file '$TEST_FILE')"
    fi
    ;;
  *) usage;;
esac
