#!/usr/bin/env bash
# audit-list-filter.sh — CI gate (KAC-219 / RBAC v2 W6).
#
# Refuses to ship a `List<Resource>` handler in `internal/apps/kacho/api/`
# that returns rows without consulting `listauthz.Adapter.ListAllowedIDs`
# (the canonical RBAC v2 list-filter port from kacho-corelib).
#
# Heuristic:
#   1. Collect every `func (h *Handler) List(...)` (or with stream name)
#      under internal/apps/kacho/api/<resource>/{handler,list}.go.
#   2. For each candidate file, also grep its sibling list.go (if any)
#      for `ListAllowedIDs` OR `listauthz.Adapter`.
#   3. If neither token is found in the handler OR its sibling list.go,
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
  if grep -qE 'ListAllowedIDs|listauthz\.Adapter' "$handler" "$SIBLING_LIST" 2>/dev/null; then
    continue
  fi
  echo "audit-list-filter: $RES — List handler missing listauthz.Adapter wiring"
  echo "  handler: $handler"
  [[ -f "$SIBLING_LIST" ]] && echo "  list.go: $SIBLING_LIST"
  FAIL=1
done

if [[ $FAIL -ne 0 ]]; then
  echo
  echo "RBAC v2 (KAC-214) requires every public List<Resource> RPC to filter"
  echo "results through listauthz.Adapter.ListAllowedIDs. Whitelist the"
  echo "handler (admin-only) with --allow=<resource> if the bypass is"
  echo "intentional."
  exit 1
fi

echo "audit-list-filter: OK"
