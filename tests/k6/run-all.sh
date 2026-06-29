#!/usr/bin/env bash
# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

# Прогон всех k6-сценариев последовательно.
set -euo pipefail
cd "$(dirname "$0")"

K6=${K6:-k6}
BASE_URL=${BASE_URL:-http://localhost:18080}
PROJECT_ID=${PROJECT_ID:-b1gc03zgwksmpe92fd5t}
ZONE_ID=${ZONE_ID:-zone-a}

mkdir -p results

FAILED=()

run() {
  local name="$1"
  echo "===== $name ====="
  # Снимаем errexit вокруг пайпа: берем PIPESTATUS[0] (k6), а не статус tee —
  # иначе нарушенный порог (k6 возвращает non-zero) был бы замаскирован.
  set +e
  $K6 run \
    --env BASE_URL="$BASE_URL" \
    --env PROJECT_ID="$PROJECT_ID" \
    --env ZONE_ID="$ZONE_ID" \
    --summary-export "results/${name}.json" \
    --quiet \
    "scripts/${name}.js" 2>&1 | tee "results/${name}.log"
  local rc=${PIPESTATUS[0]}
  set -e
  if [[ "$rc" -ne 0 ]]; then
    echo "[FAIL] k6 ${name} — нарушены пороги или прогон упал (rc=${rc})"
    FAILED+=("$name")
  fi
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

if [[ "${#FAILED[@]}" -gt 0 ]]; then
  echo "FAIL: k6-сценарии с нарушенными порогами: ${FAILED[*]}" >&2
  exit 1
fi
