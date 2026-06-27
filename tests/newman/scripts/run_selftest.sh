#!/usr/bin/env bash
# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

# tests/newman/scripts/run_selftest.sh — самопроверка честности агрегатора run.sh.
#
# Доказывает: коллекция с проваленным ассертом (assertions.failed>0), ненулевым
# exit-кодом newman или отсутствующая коллекция ОБЯЗАНА давать ненулевой вердикт.
# Гоняет реальную функцию aggregate_verdict из run.sh на синтетических
# newman-JSON — без установленного newman и без стенда.
#
# Дополнительно показывает RED→GREEN: прежнее поведение (печать сводки без
# exit-кода) маскировало провал (вердикт 0), новое — ловит его (вердикт 1).

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=run.sh
source "$HERE/run.sh"   # main() не запускается (guard по BASH_SOURCE)

command -v jq >/dev/null 2>&1 || { echo "SELFTEST SKIP: нет jq"; exit 0; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Синтетический newman JSON-reporter с заданным числом проваленных ассертов.
mk_json() { # <stem> <failed>
  local stem="$1" failed="$2"
  printf '{"run":{"stats":{"assertions":{"total":10,"failed":%s},"requests":{"total":7}}}}\n' \
    "$failed" > "$TMP/${stem}.json"
}

# Реконструкция ПРЕЖНЕГО хвоста run.sh: печатает сводку и всегда возвращает 0
# (именно так провал маскировался). Нужна только для контраста RED→GREEN.
legacy_print_summary() { # <out_dir> <stem>...
  local out_dir="$1"; shift
  local stem f
  for stem in "$@"; do
    f="${out_dir}/${stem}.json"
    [[ -f "$f" ]] || continue
    jq -r '"\(.run.stats.assertions.failed) failed"' "$f" >/dev/null 2>&1 || true
  done
  return 0
}

fails=0
expect() { # <desc> <want-rc> <got-rc>
  if [[ "$2" == "$3" ]]; then
    printf 'ok   — %s (rc=%s)\n' "$1" "$3"
  else
    printf 'FAIL — %s (ожидался rc=%s, получили rc=%s)\n' "$1" "$2" "$3"
    fails=1
  fi
}

# Фикстуры.
mk_json green 0; echo 0 > "$TMP/green.rc"
mk_json bad   3; echo 0 > "$TMP/bad.rc"
mk_json crash 0; echo 1 > "$TMP/crash.rc"

echo "--- RED→GREEN на провальной коллекции (failed=3) ---"
legacy_print_summary "$TMP" bad; rc=$?
expect "RED: прежний summary-only маскирует провал (verdict=0)" 0 "$rc"
aggregate_verdict "$TMP" bad >/dev/null; rc=$?
expect "GREEN: новый aggregate_verdict ловит провал (verdict=1)" 1 "$rc"

echo "--- свойства вердикта ---"
aggregate_verdict "$TMP" green >/dev/null; rc=$?
expect "all-green → 0" 0 "$rc"
aggregate_verdict "$TMP" crash >/dev/null; rc=$?
expect "ненулевой exit newman → 1" 1 "$rc"
aggregate_verdict "$TMP" absent >/dev/null; rc=$?
expect "отсутствующая коллекция → 1" 1 "$rc"
aggregate_verdict "$TMP" green bad >/dev/null; rc=$?
expect "mixed green+bad → 1" 1 "$rc"

echo
if [[ "$fails" == 0 ]]; then
  echo "SELFTEST: PASS"
  exit 0
else
  echo "SELFTEST: FAIL"
  exit 1
fi
