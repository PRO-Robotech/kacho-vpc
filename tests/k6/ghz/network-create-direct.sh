#!/usr/bin/env bash
# ghz: прямой gRPC load test Network.Create на kacho-vpc:9090 (минуя api-gateway).
# Требует port-forward: kubectl -n kacho port-forward svc/vpc 19090:9090
#
# Подтверждённый baseline (2026-05-11): 5778 Create/sec на 1 pod при:
#   - synchronous_commit=off (Postgres)
#   - KACHO_VPC_DB_MAX_CONNS=280 (pgxpool)
#   - KACHO_VPC_DEFAULT_SG_INLINE=false
#   - folder existence TTL cache (30s)
#   - pg_notify trigger disabled (для чистого write throughput)
set -euo pipefail

TARGET=${TARGET:-localhost:19090}
TOTAL=${TOTAL:-300000}
CONCURRENCY=${CONCURRENCY:-300}
CONNECTIONS=${CONNECTIONS:-15}
FOLDER_ID=${FOLDER_ID:-b1gc03zgwksmpe92fd5t}

echo "ghz Network.Create — total=$TOTAL concurrency=$CONCURRENCY connections=$CONNECTIONS target=$TARGET"
ghz --insecure \
  --call kacho.cloud.vpc.v1.NetworkService.Create \
  --total "$TOTAL" --concurrency "$CONCURRENCY" --connections "$CONNECTIONS" \
  --metadata "{\"x-kacho-actor\":\"ghz-loadtest\",\"x-kacho-folder-id\":\"$FOLDER_ID\"}" \
  -d "{\"folder_id\":\"$FOLDER_ID\",\"name\":\"ghz-{{.RequestNumber}}-{{.TimestampUnixNano}}\"}" \
  "$TARGET"

echo
echo "Cleanup: psql -c \"DELETE FROM networks WHERE name LIKE 'ghz-%'; DELETE FROM operations WHERE description LIKE 'Create network ghz-%';\""
