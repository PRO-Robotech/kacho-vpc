#!/usr/bin/env bash
# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

# tests/newman/scripts/run.sh — прогон newman-коллекций с честным exit-кодом.
#
# Usage:
#   ./scripts/run.sh                          # все коллекции, сводный отчет
#   ./scripts/run.sh --service network        # одна коллекция
#   ./scripts/run.sh --service network --bail # прерывать после первого fail
#   ./scripts/run.sh --delay 100              # задержка между запросами (ms)
#   ./scripts/run.sh --jobs 2                 # макс. параллельных коллекций (default 4)
#
# Набор коллекций определяется автоматически: объединение source-of-truth
# cases/*.py (gen.py делает 1:1 коллекцию на каждый case-файл) и реально
# присутствующих collections/*.postman_collection.json. Так ни одна коллекция
# не пропускается молча, а отсутствие ожидаемой (cases/<x>.py есть, а
# collections/<x>.json нет) фиксируется как MISSING и валит прогон.
#
# Per-service коллекции гоняются параллельно (cap --jobs, default 4): каждая
# коллекция изолирует свои ресурсы по {{runId}}-суффиксам внутри общего
# existingProjectId, так что параллельный прогон безопасен.
#
# Exit-код: 0 только если у каждой коллекции assertions.failed==0, exit-код
# newman==0 и коллекция присутствует. Любой провал/краш/таймаут/отсутствие →
# exit 1. Сводка печатается всегда (out/summary.txt).
#
# Outputs:
#   out/<service>.json — newman JSON reporter (для агрегации)
#   out/<service>.cli  — newman cli-вывод
#   out/<service>.rc   — exit-код newman конкретной коллекции
#   out/summary.txt    — итоговая сводка

# Каталог tests/newman (на уровень выше scripts/). Считается при загрузке файла,
# поэтому одинаково корректен и при прямом запуске, и при `source` из self-test.
NEWMAN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# expected_stems — ожидаемый набор коллекций: basename каждого cases/*.py.
expected_stems() {
  local f stem
  for f in "$NEWMAN_DIR"/cases/*.py; do
    [[ -e "$f" ]] || continue
    stem="$(basename "$f" .py)"
    case "$stem" in __init__|__main__) continue ;; esac
    printf '%s\n' "$stem"
  done
}

# present_stems — фактически сгенерированные коллекции collections/*.json.
present_stems() {
  local f stem
  for f in "$NEWMAN_DIR"/collections/*.postman_collection.json; do
    [[ -e "$f" ]] || continue
    stem="$(basename "$f" .postman_collection.json)"
    printf '%s\n' "$stem"
  done
}

# run_one — прогон одной коллекции. Пишет out/<svc>.json|.cli|.rc.
# Использует глобальные ENV/DELAY/BAIL/EXTRA, выставленные в main().
run_one() {
  local svc="$1"
  local col="collections/${svc}.postman_collection.json"
  if [[ ! -f "$col" ]]; then
    echo "[missing] ${svc} — нет коллекции ${col}"
    echo "missing" > "out/${svc}.rc"
    return 0
  fi
  echo "===== ${svc} ====="
  # Снимаем errexit вокруг пайпа, чтобы провал newman (через pipefail) не убил
  # фоновый сабшелл до того, как мы зафиксируем его реальный exit-код. Берем
  # именно PIPESTATUS[0] (newman), а НЕ статус tee — иначе провал маскируется.
  set +e
  newman run "$col" \
    -e "$ENV" \
    --delay-request "$DELAY" \
    $BAIL \
    --reporters cli,json \
    --reporter-json-export "out/${svc}.json" \
    ${EXTRA[@]+"${EXTRA[@]}"} 2>&1 | tee "out/${svc}.cli"
  local rc=${PIPESTATUS[0]}
  set -e
  echo "$rc" > "out/${svc}.rc"
  return 0
}

# aggregate_verdict — чистая, тестируемая функция вердикта.
#   aggregate_verdict <out_dir> <stem>...
# Печатает сводную таблицу и возвращает 1, если у любого stem: отсутствует
# out/<stem>.json (MISSING), assertions.failed>0 или rc!=0. Иначе 0.
aggregate_verdict() {
  local out_dir="$1"; shift
  local bad=0 stem json rcfile rc total failed requests
  printf "%-25s %10s %10s %10s %8s\n" "COLLECTION" "ASSERT" "FAILED" "REQUESTS" "RC"
  for stem in "$@"; do
    json="${out_dir}/${stem}.json"
    rcfile="${out_dir}/${stem}.rc"
    rc="n/a"
    [[ -f "$rcfile" ]] && rc="$(cat "$rcfile")"
    if [[ ! -f "$json" ]]; then
      printf "%-25s %10s %10s %10s %8s\n" "$stem" "-" "-" "-" "MISSING"
      bad=1
      continue
    fi
    total=0; failed=0; requests=0
    read -r total failed requests < <(
      jq -r '"\(.run.stats.assertions.total) \(.run.stats.assertions.failed) \(.run.stats.requests.total)"' \
        "$json" 2>/dev/null || echo "0 0 0"
    )
    [[ "$total" =~ ^[0-9]+$ ]]    || total=0
    [[ "$failed" =~ ^[0-9]+$ ]]   || failed=0
    [[ "$requests" =~ ^[0-9]+$ ]] || requests=0
    printf "%-25s %10s %10s %10s %8s\n" "$stem" "$total" "$failed" "$requests" "$rc"
    [[ "$failed" -gt 0 ]] && bad=1
    [[ "$rc" == "0" ]]    || bad=1
  done
  return "$bad"
}

main() {
  set -euo pipefail
  cd "$NEWMAN_DIR"

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
  [[ -f "$ENV" ]] || { echo "missing env: $ENV" >&2; exit 1; }

  mkdir -p out
  # Свежий прогон: убираем артефакты прошлого, чтобы stale-json не маскировал
  # выпавшую коллекцию (rm -f не падает на отсутствующих файлах).
  rm -f out/*.json out/*.cli out/*.rc out/summary.txt 2>/dev/null

  # Набор stems для прогона и для вердикта.
  local -a stems=()
  if [[ -n "$SERVICE" ]]; then
    stems=("$SERVICE")
  else
    local s
    while IFS= read -r s; do
      [[ -n "$s" ]] && stems+=("$s")
    done < <( { expected_stems; present_stems; } | sort -u )
  fi

  # Параллельный прогон с cap=$JOBS. Каждая коллекция runId-scoped → safe.
  local svc
  for svc in "${stems[@]}"; do
    while [[ "$(jobs -rp | wc -l)" -ge "$JOBS" ]]; do wait -n; done
    run_one "$svc" &
  done
  wait

  echo
  echo "===== Summary ====="
  # pipefail прокидывает ненулевой вердикт сквозь tee — печать сводки сохранена.
  if aggregate_verdict "out" "${stems[@]}" | tee out/summary.txt; then
    echo "OK: все коллекции зеленые."
  else
    echo "FAIL: одна или несколько коллекций провалены / отсутствуют (см. таблицу выше)." >&2
    exit 1
  fi
}

# main запускается только при прямом вызове; при `source` (self-test) — нет.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
