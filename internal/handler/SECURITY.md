# Security TODOs (handler layer)

## Tenant isolation (IDOR) — must add before AuthN merge

Сейчас все public handlers (address/network/subnet/route_table/
security_group/gateway/private_endpoint) принимают opaque ID и не сверяют
`folder_id` ресурса против caller-identity (AuthN noop = anonymous,
любой client может Get/Update/Delete любой ресурс по известному ID).

**При подключении IAM** (отдельный PR) каждый Get/Update/Delete handler
должен:

1. Извлечь caller-folder из gRPC metadata (Bearer token → claims → folders).
2. После `repo.Get(ctx, id)` сверить `resource.FolderID` ∈ caller-folders.
3. Не совпадает → `PermissionDenied` (verbatim YC: "Permission denied").

Точки куда добавлять (по handler'ам):

| Handler | Methods needing folder-check |
|---|---|
| address_handler.go | Get, Update, Delete, Move, GetByValue |
| network_handler.go | Get, Update, Delete, Move, ListSubnets, ListSecurityGroups, ListRouteTables, ListOperations |
| subnet_handler.go | Get, Update, Delete, Move, AddCidrBlocks, RemoveCidrBlocks, Relocate, ListUsedAddresses |
| route_table_handler.go | Get, Update, Delete, Move |
| security_group_handler.go | Get, Update, Delete, Move, UpdateRules, UpdateRule |
| gateway_handler.go | Get, Update, Delete, Move |
| private_endpoint_handler.go | Get, Update, Delete, Move |

## Internal-port (:9091) lateral movement

Internal* RPC сейчас не имеют mTLS / NetworkPolicy / shared-secret.
Любой pod в namespace `kacho` может достучаться. Watch RPC раздаёт весь
outbox без folder filter (mass exfiltration vector).

Fix-варианты (см. workspace CLAUDE.md):
- NetworkPolicy в `kacho-deploy/helm/.../templates/networkpolicy.yaml`,
  ограничивающая :9091 только api-gateway и admin-tooling pods.
- mTLS interceptor в `kacho-corelib/grpcsrv` с allowlist'ом
  ServiceAccount'а (через SPIFFE/SPIRE или k8s service-account-tokens).
- `InternalWatchService.Watch(folder_filter)` — добавить required filter,
  иначе reject. Или если admin — отдельный admin-only RPC.

## DB plaintext (sslmode=disable)

`internal/config/config.go` хардкодит `sslmode=disable` в DSN. Cluster-
internal traffic к Postgres плейнтекст. Fix: env-override
`KACHO_VPC_DB_SSLMODE` (default `verify-full` в production helm values).

## Cross-service plaintext (insecure gRPC)

`cmd/vpc/main.go` использует `grpc.WithTransportCredentials(insecure.NewCredentials())` для FolderClient.
В namespace MITM может подделать ответ FolderService.Get → чужой folder
"легитимизируется". Fix: TLS на gRPC server-side (resource-manager) +
client-side credentials в kacho-vpc.

## Raw error leak в Internal handlers

`internal_address_handler.go` / `internal_watch_handler.go` шлют
`status.Errorf(codes.Internal, "begin tx: %v", err)` — pgx ошибки могут
содержать имя hostname/IP/DB/table/query-fragment. Mapping discipline в
public-сервисах (`mapRepoErr` + `stripSentinel`) тут отсутствует.
Fix: общий `internalMapErr(err)` для всех Internal handler'ов.
