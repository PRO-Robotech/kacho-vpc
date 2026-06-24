#!/usr/bin/env bash
# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

# audit-list-filter.sh — CI gate для per-object list-filter в RBAC.
#
# Refuses to ship a `List<Resource>` handler in `internal/apps/kacho/api/`
# that returns rows without consulting the per-object list-filter
# `ListAllowedIDs` (authzfilter.UseCasePort → AuthorizeService.ListObjects).
#
# Per-object filtered List (NOT project-level all-or-nothing): the List
# use-case must call `ListAllowedIDs(viewer)` and narrow rows via
# `repo.ListByIDs` (WHERE id = ANY). read==enforce.
#
# Heuristic:
#   1. Collect every `func (h *Handler) List(...)` (or with stream name)
#      under internal/apps/kacho/api/<resource>/{handler,list}.go.
#   2. For each candidate file, also grep its sibling list.go (if any)
#      for `ListAllowedIDs`.
#   3. If the token is not found in the handler OR its sibling list.go,
#      print the candidate path and exit 1.
#
# Whitelisted (admin-only resources where every authenticated caller is
# expected to see every row):
#   - addresspool   — Internal/admin RPC scoped to system_admin in middleware
#
# Override:
#   tools/audit-list-filter.sh --allow="<resource>" extends the whitelist.

set -euo pipefail

WHITELIST=("addresspool")
while [[ ${1:-} == --allow=* ]]; do
  WHITELIST+=("${1#--allow=}")
  shift || true
done

is_whitelisted() {
  local r=$1
  for w in "${WHITELIST[@]}"; do [[ "$w" == "$r" ]] && return 0; done
  return 1
}

ROOT=internal/apps/kacho/api
if [[ ! -d "$ROOT" ]]; then
  echo "audit-list-filter: not in a kacho-{vpc,compute} repo (no $ROOT)" >&2
  exit 0
fi

FAIL=0
for handler in $(grep -rl 'func .* List(' --include='handler.go' "$ROOT" 2>/dev/null); do
  RES=$(basename "$(dirname "$handler")")
  is_whitelisted "$RES" && continue
  SIBLING_LIST="$(dirname "$handler")/list.go"
  # Per-object filter обязателен: List должен прогонять id-set через per-object
  # `ListAllowedIDs` (authzfilter.UseCasePort → AuthorizeService.ListObjects) и
  # сужать repo через ListByIDs. Project-level all-or-nothing (`CanViewProject`)
  # больше НЕ принимается.
  if grep -qE 'ListAllowedIDs' "$handler" "$SIBLING_LIST" 2>/dev/null; then
    continue
  fi
  echo "audit-list-filter: $RES — List handler missing per-object list-filter (ListAllowedIDs)"
  echo "  handler: $handler"
  [[ -f "$SIBLING_LIST" ]] && echo "  list.go: $SIBLING_LIST"
  FAIL=1
done

if [[ $FAIL -ne 0 ]]; then
  echo
  echo "Every public List<Resource> RPC must gate results through listauthz"
  echo "(ListAllowedIDs or CanViewProject)."
  echo "Whitelist the handler (admin-only) with --allow=<resource> if the"
  echo "bypass is intentional."
  exit 1
fi

echo "audit-list-filter: OK"
