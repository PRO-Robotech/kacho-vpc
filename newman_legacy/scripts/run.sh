#!/usr/bin/env bash
# run.sh — quota-aware entrypoint for the VPC Newman regression suite.
#
# Pipeline (по умолчанию):
#   1. cleanup-vpc.sh -y     → освобождает baseline в FOLDER/FOLDER_CROSS
#   2. RO     suite (~30 кейсов, --delay-request 50)
#   3. LIGHT  suite (~70 кейсов, --delay-request 250)
#   4. SEQ    suite (~10 кейсов, --delay-request 1500, critical-quota)
#
# Sub-suites — отдельные коллекции kacho-vpc-{ro,light,seq}.postman_collection.json,
# создаются `scripts/build-suite.py` из `kacho-vpc.postman_collection.json` (источник).
#
# Usage:
#   ./scripts/run.sh                       # full pipeline (env=yc по умолчанию)
#   ./scripts/run.sh --suite ro            # только RO
#   ./scripts/run.sh --suite light
#   ./scripts/run.sh --suite seq
#   ./scripts/run.sh --no-precleanup       # пропустить cleanup-vpc.sh перед прогоном
#   ./scripts/run.sh --no-rebuild          # не вызывать build-suite.py перед прогоном
#   ./scripts/run.sh --folder NET-CR-OK    # одна папка (только в --suite light по умолчанию)
#   ./scripts/run.sh --env local           # против Kachō (port-forward api-gateway)
#
# Requirements:
#   - newman: npm i -g newman (или npx newman ...).
#   - yc CLI: $YC_BIN (default /home/dk/yandex-cloud/bin/yc), нужен только для --env yc.
#   - environments/yc.postman_environment.json с existingFolderId / existingFolderCrossId.

set -euo pipefail
cd "$(dirname "$0")/.."

ENV="yc"
SUITE="all"        # all | ro | light | seq
DO_PRECLEAN=1
DO_REBUILD=1
FOLDER=""
EXTRA=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env)            ENV="$2"; shift 2;;
    --suite)          SUITE="$2"; shift 2;;
    --no-precleanup)  DO_PRECLEAN=0; shift;;
    --no-rebuild)     DO_REBUILD=0; shift;;
    --folder)         FOLDER="$2"; shift 2;;
    *)                EXTRA+=("$1"); shift;;
  esac
done

ENV_FILE="environments/${ENV}.postman_environment.json"
[[ -f "$ENV_FILE" ]] || { echo "ERROR: env not found: $ENV_FILE" >&2; exit 2; }

mkdir -p out

# --- Pre-cleanup (yc only) -----------------------------------------------
if [[ "$ENV" == "yc" && "$DO_PRECLEAN" == "1" ]]; then
  echo "==> pre-cleanup via cleanup-vpc.sh (both folders)..."
  ./scripts/cleanup-vpc.sh -y || { echo "ERROR: pre-cleanup failed" >&2; exit 2; }
fi

# --- Rebuild sub-suites --------------------------------------------------
if [[ "$DO_REBUILD" == "1" ]]; then
  echo "==> regenerating sub-suites..."
  python3 scripts/build-suite.py
fi

# --- IAM token injection (yc) --------------------------------------------
ENV_TMP=""
if [[ "$ENV" == "yc" ]]; then
  YC_BIN="${YC_BIN:-/home/dk/yandex-cloud/bin/yc}"
  [[ -x "$YC_BIN" ]] || { echo "ERROR: yc binary not at $YC_BIN" >&2; exit 2; }
  TOKEN="$($YC_BIN iam create-token 2>/dev/null)"
  [[ ${#TOKEN} -ge 100 ]] || { echo "ERROR: failed to obtain IAM token" >&2; exit 2; }
  echo "==> obtained IAM token (${#TOKEN} chars)"

  ENV_TMP="$(mktemp -p out yc-env.XXXXXX.json)"
  trap 'rm -f "$ENV_TMP"' EXIT
  python3 - "$ENV_FILE" "$ENV_TMP" "$TOKEN" <<'PY'
import json, pathlib, sys
src, dst, tok = sys.argv[1:4]
env = json.loads(pathlib.Path(src).read_text())
for v in env["values"]:
    if v["key"] == "ycToken":
        v["value"] = tok
pathlib.Path(dst).write_text(json.dumps(env, indent=2))
PY
  ENV_FILE="$ENV_TMP"
fi

# --- Newman runner -------------------------------------------------------
run_suite() {
  local label="$1" coll="$2" delay="$3" json_out="$4"
  echo
  echo "==================================================================="
  echo "==> SUITE: $label    delay=${delay}ms    coll=$coll"
  echo "==================================================================="
  local args=(
    run "$coll"
    -e "$ENV_FILE"
    --reporters cli,json
    --reporter-json-export "$json_out"
    --delay-request "$delay"
    --color on
    --insecure
    --timeout-request 30000
  )
  if [[ -n "$FOLDER" ]]; then
    args+=(--folder "00-preflight" --folder "$FOLDER" --folder "99-teardown")
  fi
  args+=("${EXTRA[@]}")
  if command -v newman >/dev/null 2>&1; then
    newman "${args[@]}" || return $?
  else
    npx --yes newman "${args[@]}" || return $?
  fi
}

EXIT=0
case "$SUITE" in
  ro)    run_suite "RO"    "collections/kacho-vpc-ro.postman_collection.json"      25 "out/last-run-ro.json"    || EXIT=$? ;;
  light) run_suite "LIGHT" "collections/kacho-vpc-light.postman_collection.json"  200 "out/last-run-light.json" || EXIT=$? ;;
  seq)   run_suite "SEQ"   "collections/kacho-vpc-seq.postman_collection.json"   1200 "out/last-run-seq.json"   || EXIT=$? ;;
  all)
    run_suite "RO"    "collections/kacho-vpc-ro.postman_collection.json"      25 "out/last-run-ro.json"    || EXIT=$?
    run_suite "LIGHT" "collections/kacho-vpc-light.postman_collection.json"  200 "out/last-run-light.json" || EXIT=$?
    run_suite "SEQ"   "collections/kacho-vpc-seq.postman_collection.json"   1200 "out/last-run-seq.json"   || EXIT=$?
    ;;
  *)
    echo "ERROR: unknown --suite '$SUITE' (expected: all|ro|light|seq)" >&2
    exit 2
    ;;
esac

# --- Summary -------------------------------------------------------------
echo
echo "==================================================================="
echo "==> Final quota snapshot"
if [[ "$ENV" == "yc" ]]; then
  for kind in network subnet security-group route-table gateway address private-endpoint; do
    n_main=$($YC_BIN vpc "$kind" list --folder-id b1gfcs0biit7p23meig5 --format json 2>/dev/null | jq 'length')
    n_cross=$($YC_BIN vpc "$kind" list --folder-id b1gmltaq9ahkvk35gnb0 --format json 2>/dev/null | jq 'length')
    printf "  %-18s main=%-4s cross=%s\n" "$kind" "$n_main" "$n_cross"
  done
fi
echo "==================================================================="

exit $EXIT
