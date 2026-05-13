#!/usr/bin/env bash
# tests/newman/scripts/run.sh — прогон newman коллекций.
#
# Usage:
#   ./scripts/run.sh                          # все коллекции, сводный отчёт
#   ./scripts/run.sh --service network        # одна коллекция
#   ./scripts/run.sh --service network --bail # прерывать после первого fail
#   ./scripts/run.sh --delay 100              # задержка между запросами (ms)
#   ./scripts/run.sh --jobs 2                 # макс. параллельных коллекций (default 4)
#
# Per-service коллекции гоняются параллельно (cap --jobs, default 4): каждая
# коллекция изолирует свои ресурсы по {{runId}}-суффиксам внутри общего
# existingFolderId, так что параллельный прогон безопасен.
#
# Outputs:
#   out/<service>.json — newman JSON reporter (для агрегации)
#   out/<service>.cli  — newman cli-вывод
#   out/summary.txt    — итоговая сводка

set -euo pipefail
cd "$(dirname "$0")/.."

SERVICE=""
BAIL=""
DELAY="15"
JOBS="4"
EXTRA=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --service) SERVICE="$2"; shift 2 ;;
    --bail)    BAIL="--bail"; shift ;;
    --delay)   DELAY="$2"; shift 2 ;;
    --jobs)    JOBS="$2"; shift 2 ;;
    *)         EXTRA+=("$1"); shift ;;
  esac
done

ENV="environments/local.postman_environment.json"
[[ -f "$ENV" ]] || { echo "missing env: $ENV"; exit 1; }

run_one() {
  local svc="$1"
  local col="collections/${svc}.postman_collection.json"
  if [[ ! -f "$col" ]]; then
    echo "[skip] $svc — нет коллекции"
    return 0
  fi
  echo "===== ${svc} ====="
  newman run "$col" \
    -e "$ENV" \
    --delay-request "$DELAY" \
    $BAIL \
    --reporters cli,json \
    --reporter-json-export "out/${svc}.json" \
    "${EXTRA[@]}" 2>&1 | tee "out/${svc}.cli" || true
}

mkdir -p out

if [[ -n "$SERVICE" ]]; then
  run_one "$SERVICE"
else
  # Параллельный прогон с cap=$JOBS. Каждая коллекция runId-scoped → safe.
  for svc in network subnet address route-table security-group gateway private-endpoint \
             network-interface operation internal-pool internal-cloud; do
    while [[ "$(jobs -rp | wc -l)" -ge "$JOBS" ]]; do wait -n; done
    run_one "$svc" &
  done
  wait
fi

echo
echo "===== Summary ====="
{
  printf "%-25s %10s %10s %10s\n" "SERVICE" "ASSERT" "FAILED" "REQUESTS"
  for f in out/*.json; do
    [[ -f "$f" ]] || continue
    name=$(basename "$f" .json)
    stats=$(jq -r '"\(.run.stats.assertions.total) \(.run.stats.assertions.failed) \(.run.stats.requests.total)"' "$f" 2>/dev/null || echo "0 0 0")
    set -- $stats
    printf "%-25s %10s %10s %10s\n" "$name" "$1" "$2" "$3"
  done
} | tee out/summary.txt
