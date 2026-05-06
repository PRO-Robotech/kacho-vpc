#!/usr/bin/env bash
# cleanup-vpc.sh — удаляет VPC-ресурсы из двух папок (default + default-cross)
# в правильной очередности с параллельной чисткой внутри уровня.
#
# Usage:
#   ./scripts/cleanup-vpc.sh                                # default folders
#   ./scripts/cleanup-vpc.sh --folder <id>                  # override default
#   ./scripts/cleanup-vpc.sh --folder-cross <id>            # override default-cross
#   ./scripts/cleanup-vpc.sh --parallel 30                  # override xargs -P
#   ./scripts/cleanup-vpc.sh -y                             # skip confirmation
#
# Очерёдность (внутри уровня — параллельно по обеим папкам через xargs -P):
#   1. subnets
#   2. route-tables
#   3. gateways
#   4. addresses
#   5. private-endpoints
#   6. non-default security-groups (несколько проходов: cross-refs в правилах)
#   7. networks (default SG исчезает вместе с network)
#
# Cross-folder важен: SG/subnet из default-cross могут висеть на networks из
# default (и наоборот). Поэтому каждый kind чистится в обеих папках до
# перехода к следующему уровню — networks удаляются последними.

set -euo pipefail

YC_BIN="${YC_BIN:-/home/dk/yandex-cloud/bin/yc}"
FOLDER="b1gfcs0biit7p23meig5"
FOLDER_CROSS="b1gmltaq9ahkvk35gnb0"
PARALLEL="${PARALLEL:-20}"
ASSUME_YES=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --folder)        FOLDER="$2"; shift 2;;
    --folder-cross)  FOLDER_CROSS="$2"; shift 2;;
    --parallel)      PARALLEL="$2"; shift 2;;
    -y|--yes)        ASSUME_YES=1; shift;;
    -h|--help)       sed -n '2,23p' "$0"; exit 0;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done

if [[ ! -x "$YC_BIN" ]]; then
  echo "ERROR: cloud-provider CLI not found at $YC_BIN" >&2; exit 2
fi
command -v jq >/dev/null || { echo "ERROR: jq required" >&2; exit 2; }

FOLDERS=("$FOLDER" "$FOLDER_CROSS")

list_ids() {
  "$YC_BIN" vpc "$1" list --folder-id "$2" --format json 2>/dev/null \
    | jq -r '.[].id'
}
count_kind() {
  "$YC_BIN" vpc "$1" list --folder-id "$2" --format json 2>/dev/null \
    | jq 'length'
}

inventory() {
  local f="$1"
  printf "  %s: networks=%s subnets=%s sg=%s rt=%s addr=%s gw=%s pe=%s\n" \
    "$f" \
    "$(count_kind network "$f")" \
    "$(count_kind subnet "$f")" \
    "$(count_kind security-group "$f")" \
    "$(count_kind route-table "$f")" \
    "$(count_kind address "$f")" \
    "$(count_kind gateway "$f")" \
    "$(count_kind private-endpoint "$f")"
}

delete_kind_in_folder() {
  local kind="$1" f="$2"
  local ids
  ids=$(list_ids "$kind" "$f")
  [[ -z "$ids" ]] && return 0
  echo "$ids" | xargs -P "$PARALLEL" -I{} sh -c \
    "'$YC_BIN' vpc $kind delete {} >/dev/null 2>&1 || echo '  FAIL [$kind $f] {}'"
}

defaults_for() {
  "$YC_BIN" vpc network list --folder-id "$1" --format json 2>/dev/null \
    | jq -r '.[].default_security_group_id // empty' | sort -u
}
non_default_sgs() {
  local f="$1"
  comm -23 \
    <("$YC_BIN" vpc security-group list --folder-id "$f" --format json 2>/dev/null \
        | jq -r '.[].id' | sort) \
    <(defaults_for "$f") || true
}

sum_kind() {
  local kind="$1" total=0 c
  for f in "${FOLDERS[@]}"; do
    c=$(count_kind "$kind" "$f")
    total=$((total + c))
  done
  echo "$total"
}
sum_non_default_sgs() {
  local total=0 ids cnt
  for f in "${FOLDERS[@]}"; do
    ids=$(non_default_sgs "$f")
    if [[ -z "$ids" ]]; then cnt=0; else cnt=$(echo "$ids" | wc -l); fi
    total=$((total + cnt))
  done
  echo "$total"
}

echo "=== inventory before ==="
for f in "${FOLDERS[@]}"; do inventory "$f"; done

# Pre-flight totals per kind — чтобы скипать пустые уровни и весь скрипт.
declare -A TOTAL
for kind in subnet route-table gateway address private-endpoint network; do
  TOTAL[$kind]=$(sum_kind "$kind")
done
TOTAL[non-default-sg]=$(sum_non_default_sgs)

GRAND=0
for k in "${!TOTAL[@]}"; do GRAND=$((GRAND + TOTAL[$k])); done

if [[ $GRAND -eq 0 ]]; then
  echo
  echo "nothing to delete — все группы по нулям. exit."
  exit 0
fi

if [[ $ASSUME_YES -ne 1 ]]; then
  echo
  read -rp "Удалить всё VPC из этих папок? [y/N] " ans
  [[ "$ans" =~ ^[yY]$ ]] || { echo "aborted"; exit 1; }
fi

# --- 1..5: per-folder ресурсы (параллельно по папкам) ---
for kind in subnet route-table gateway address private-endpoint; do
  if [[ "${TOTAL[$kind]}" -eq 0 ]]; then
    echo "=== skipping $kind (0 in all folders) ==="
    continue
  fi
  echo "=== deleting $kind (total=${TOTAL[$kind]}) ==="
  for f in "${FOLDERS[@]}"; do
    [[ "$(count_kind "$kind" "$f")" -eq 0 ]] && continue
    delete_kind_in_folder "$kind" "$f" &
  done
  wait
  for f in "${FOLDERS[@]}"; do
    echo "  remaining $kind in $f: $(count_kind "$kind" "$f")"
  done
done

# --- 6: non-default SG (multi-pass — cross-references в rules) ---
if [[ "${TOTAL[non-default-sg]}" -eq 0 ]]; then
  echo "=== skipping non-default security-groups (0 in all folders) ==="
else
  echo "=== deleting non-default security-groups (total=${TOTAL[non-default-sg]}) ==="
  for pass in 1 2 3; do
    total_left=0
    for f in "${FOLDERS[@]}"; do
      ids=$(non_default_sgs "$f")
      if [[ -z "$ids" ]]; then cnt=0; else cnt=$(echo "$ids" | wc -l); fi
      total_left=$((total_left + cnt))
      [[ "$cnt" -eq 0 ]] && continue
      echo "  pass $pass [$f]: $cnt to delete"
      echo "$ids" | xargs -P "$PARALLEL" -I{} sh -c \
        "'$YC_BIN' vpc security-group delete {} >/dev/null 2>&1 || true"
    done
    [[ "$total_left" -eq 0 ]] && break
  done
  for f in "${FOLDERS[@]}"; do
    ids=$(non_default_sgs "$f")
    if [[ -z "$ids" ]]; then cnt=0; else cnt=$(echo "$ids" | wc -l); fi
    echo "  remaining non-default sg in $f: $cnt"
  done
fi

# --- 7: networks (после кросс-folder SG cleanup) ---
if [[ "${TOTAL[network]}" -eq 0 ]]; then
  echo "=== skipping networks (0 in all folders) ==="
else
  echo "=== deleting networks (total=${TOTAL[network]}) ==="
  for pass in 1 2; do
    total_left=0
    for f in "${FOLDERS[@]}"; do
      ids=$(list_ids network "$f")
      if [[ -z "$ids" ]]; then cnt=0; else cnt=$(echo "$ids" | wc -l); fi
      total_left=$((total_left + cnt))
      [[ "$cnt" -eq 0 ]] && continue
      echo "  pass $pass [$f]: $cnt to delete"
      echo "$ids" | xargs -P "$PARALLEL" -I{} sh -c \
        "'$YC_BIN' vpc network delete {} >/dev/null 2>&1 || echo '  FAIL [network $f] {}'"
    done
    [[ "$total_left" -eq 0 ]] && break
  done
fi

echo
echo "=== inventory after ==="
for f in "${FOLDERS[@]}"; do inventory "$f"; done
