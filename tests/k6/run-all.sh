#!/usr/bin/env bash
# Прогон всех k6-сценариев последовательно.
set -euo pipefail
cd "$(dirname "$0")"

K6=${K6:-k6}
BASE_URL=${BASE_URL:-http://localhost:18080}
FOLDER_ID=${FOLDER_ID:-b1gc03zgwksmpe92fd5t}
ZONE_ID=${ZONE_ID:-ru-central1-a}

mkdir -p results

run() {
  local name="$1"
  echo "===== $name ====="
  $K6 run \
    --env BASE_URL="$BASE_URL" \
    --env FOLDER_ID="$FOLDER_ID" \
    --env ZONE_ID="$ZONE_ID" \
    --summary-export "results/${name}.json" \
    --quiet \
    "scripts/${name}.js" 2>&1 | tee "results/${name}.log" || true
  echo
}

# Light → medium → stress
run list-heavy
run network-create-burst
run allocate-external-burst
run mixed-read-write
run breakpoint

echo "===== Results saved to results/ ====="
ls -la results/
