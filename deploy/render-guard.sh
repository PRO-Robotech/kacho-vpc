#!/usr/bin/env bash
# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

#
# render-guard.sh — helm-render assertions for the kacho-vpc deploy chart.
#
# Guards the vpc→geo edge wiring: the edge MUST dial the geo k8s Service name
# `kacho-geo` (whose server-cert SAN covers kacho-geo.* / kacho-geo-internal.*),
# NOT the bare `geo.kacho...` host — that host neither resolves nor passes TLS
# serverName verification.
#
# Asserts, against `helm template` with mtls.edges.geo=true:
#   1. the rendered ConfigMap dials extapi.geo.endpoint = kacho-geo.kacho.svc.cluster.local:9090
#   2. the rendered Deployment sets KACHO_VPC_GEO_MTLS_SERVERNAME = kacho-geo.kacho.svc.cluster.local
#   3. the old `geo.kacho.svc.cluster.local` host appears NOWHERE in the render.
#
# Usage: deploy/render-guard.sh   (run from the chart's parent, i.e. repo root or deploy/)
# Exit 0 = all assertions pass; non-zero = a guard failed.
set -euo pipefail

HELM_BIN="${HELM_BIN:-helm}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="$SCRIPT_DIR"

GOOD_HOST="kacho-geo.kacho.svc.cluster.local"
BAD_HOST="geo.kacho.svc.cluster.local"

fail() { echo "render-guard: FAIL: $*" >&2; exit 1; }

RENDER="$("$HELM_BIN" template vpc "$CHART_DIR" \
  --set mtls.enable=true \
  --set mtls.edges.geo=true)"

# 1. ConfigMap geo endpoint dials the corrected Service host on the public :9090 listener.
echo "$RENDER" | grep -qE "endpoint:[[:space:]]*\"${GOOD_HOST}:9090\"" \
  || fail "ConfigMap geo endpoint is not ${GOOD_HOST}:9090"

# 2. Deployment mTLS serverName for the geo edge is the corrected Service host.
echo "$RENDER" | grep -qE "value:[[:space:]]*\"${GOOD_HOST}\"" \
  || fail "Deployment KACHO_VPC_GEO_MTLS_SERVERNAME is not ${GOOD_HOST}"

# 3. The bare `geo.kacho...` host must not appear anywhere (configmap endpoint
#    OR mtls serverName). Anchor the leading boundary to a non-host char
#    ([^-a-zA-Z0-9]) so the correct host `kacho-geo.kacho...` (where `geo`
#    follows `-`) is NOT a false positive — only a standalone `geo.kacho...`
#    token is rejected.
if echo "$RENDER" | grep -qE "(^|[^-a-zA-Z0-9])${BAD_HOST//./\\.}"; then
  echo "$RENDER" | grep -nE "(^|[^-a-zA-Z0-9])${BAD_HOST//./\\.}" >&2
  fail "old wrong geo host '${BAD_HOST}' still present in rendered manifests"
fi

echo "render-guard: OK — vpc→geo dials ${GOOD_HOST} (Service + cert SAN)"
