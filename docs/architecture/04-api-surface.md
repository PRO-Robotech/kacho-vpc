# 04 — API Surface

Полный список RPC kacho-vpc + соответствующие REST endpoints. На сегодня
**89 RPC методов** в 13 proto-сервисах (7 public verbatim-YC + 6 internal kacho-only).

## Сводка

| Категория | Сервисов | RPC | Listener | REST exposed |
|---|---:|---:|---|---|
| Public verbatim-YC | 7 | 59 | `:9090` (public gRPC) | ✅ да, через api-gateway |
| Internal admin (kacho-only) | 5 | 29 | `:9091` (internal gRPC) | ✅ выборочно (CRUD + admin actions) |
| Outbox stream | 1 | 1 | `:9091` | ❌ только server-to-server |
| **Итого** | **13** | **89** | | **80 REST endpoints** |

## Public сервисы (`:9090`, verbatim YC)

| Сервис | RPC count | Что делает |
|---|---:|---|
| `NetworkService` | 10 | CRUD + Move + ListSubnets + ListSecurityGroups + ListRouteTables + ListOperations |
| `SubnetService` | 11 | CRUD + Move + AddCidrBlocks + RemoveCidrBlocks + Relocate + ListUsedAddresses + ListOperations |
| `AddressService` | 9 | CRUD + Move + GetByValue + ListOperations |
| `RouteTableService` | 7 | CRUD + Move + ListOperations |
| `SecurityGroupService` | 9 | CRUD + Move + UpdateRules + UpdateRule + ListOperations |
| `GatewayService` | 7 | CRUD + Move + ListOperations |
| `PrivateEndpointService` | 6 | CRUD + Move |

REST mapping — `google.api.http` аннотации в proto, см. `kacho-proto/proto/kacho/cloud/vpc/v1/<resource>_service.proto`.

## Internal admin сервисы (`:9091`, kacho-only)

| Сервис | RPC count | Что делает |
|---|---:|---|
| `InternalRegionService` | 5 | CRUD регионов |
| `InternalZoneService` | 5 | CRUD зон (с FK на регион) |
| `InternalAddressPoolService` | 13 | CRUD пулов + bindings (network/address override) + diagnostics (Check, ExplainResolution) + observability (ListAddresses, GetUtilization) |
| `InternalCloudService` | 3 | SetPoolSelector / Unset / Get на Cloud |
| `InternalNetworkService` | 1 | SetDefaultSecurityGroupId (computed-field setter) |
| `InternalAddressService` | 3 | AllocateInternalIP / AllocateExternalIP / SetIP (legacy) |
| `InternalWatchService` | 1 | Watch outbox stream |

## REST endpoints (через api-gateway)

### Public (verbatim-YC, exposed на оба listener'а)

```
# Network
GET    /vpc/v1/networks?folderId=
POST   /vpc/v1/networks                              → Operation
GET    /vpc/v1/networks/{network_id}
PATCH  /vpc/v1/networks/{network_id}                 → Operation
DELETE /vpc/v1/networks/{network_id}                 → Operation
GET    /vpc/v1/networks/{network_id}/subnets
GET    /vpc/v1/networks/{network_id}/security_groups   # snake_case в child-list!
GET    /vpc/v1/networks/{network_id}/route_tables      # snake_case!
GET    /vpc/v1/networks/{network_id}/operations
POST   /vpc/v1/networks/{network_id}:move            → Operation

# Subnet (analogously)
GET/POST/PATCH/DELETE /vpc/v1/subnets[/{id}]
GET    /vpc/v1/subnets/{subnet_id}/addresses         (UsedAddress[])
GET    /vpc/v1/subnets/{subnet_id}/operations
POST   /vpc/v1/subnets/{subnet_id}:add-cidr-blocks   # kebab-case с двоеточием!
POST   /vpc/v1/subnets/{subnet_id}:remove-cidr-blocks
POST   /vpc/v1/subnets/{subnet_id}:relocate
POST   /vpc/v1/subnets/{subnet_id}:move

# Address
GET/POST/PATCH/DELETE /vpc/v1/addresses[/{id}]
GET    /vpc/v1/addresses:byValue?value=<ip>
POST   /vpc/v1/addresses/{address_id}:move

# RouteTable (top-level — camelCase routeTables)
GET/POST/PATCH/DELETE /vpc/v1/routeTables[/{id}]

# SecurityGroup
GET/POST/PATCH/DELETE /vpc/v1/securityGroups[/{id}]
PATCH  /vpc/v1/securityGroups/{sg_id}/rules           # UpdateRules — PATCH на /rules
PATCH  /vpc/v1/securityGroups/{sg_id}/rules/{rule_id} # UpdateRule

# Gateway
GET/POST/PATCH/DELETE /vpc/v1/gateways[/{id}]

# PrivateEndpoint — путь /endpoints, НЕ /privateEndpoints!
GET/POST/PATCH/DELETE /vpc/v1/endpoints[/{id}]
GET    /vpc/v1/endpoints/{private_endpoint_id}/operations
```

> ⚠️ REST-пути неоднородны (наследие proto-аннотаций, см. FINDING-002 в
> `tests/newman/docs/BUG-MAP.md`): child-list `security_groups`/`route_tables` —
> snake_case, top-level `routeTables`/`securityGroups`/`addressPools` — camelCase,
> custom-методы — kebab с двоеточием (`:add-cidr-blocks`, `:move`),
> `OperationService.Get` — `/operations/{id}` (без `/vpc/v1/`), PE — `/endpoints`.

### Admin (kacho-only, **только cluster-internal listener**)

```
# Region
GET/POST/PATCH/DELETE /vpc/v1/regions[/{region_id}]

# Zone
GET    /vpc/v1/zones?regionId=
POST   /vpc/v1/zones
GET/PATCH/DELETE /vpc/v1/zones/{zone_id}

# AddressPool
GET    /vpc/v1/addressPools?zoneId=&kind=
POST   /vpc/v1/addressPools
GET/PATCH/DELETE /vpc/v1/addressPools/{pool_id}

# AddressPool admin actions
GET    /vpc/v1/addressPools/{pool_id}/utilization
GET    /vpc/v1/addressPools/{pool_id}/addresses?folderId=
GET    /vpc/v1/addressPools:check?zoneId=
GET    /vpc/v1/addressPools:explainResolution?addressId=&networkId=

# AddressPool bindings
POST   /vpc/v1/networks/{network_id}/addressPoolBinding   {poolId}
DELETE /vpc/v1/networks/{network_id}/addressPoolBinding
POST   /vpc/v1/addresses/{address_id}/addressPoolOverride {poolId}
DELETE /vpc/v1/addresses/{address_id}/addressPoolOverride

# CloudPoolSelector (admin)
POST   /vpc/v1/clouds/{cloud_id}/poolSelector  {selector, set_by}
GET    /vpc/v1/clouds/{cloud_id}/poolSelector
DELETE /vpc/v1/clouds/{cloud_id}/poolSelector
```

⚠️ Все admin paths **не должны** быть доступны на external TLS endpoint
(`api.kacho.local:443`, advertised для `yc` CLI). См. [`06-conventions.md`](06-conventions.md#admin-boundary).

### Internal-only (НЕ через apiGW REST, gRPC server-to-server)

```
InternalAddressService.AllocateInternalIP / AllocateExternalIP / SetIP
InternalWatchService.Watch
InternalNetworkService.SetDefaultSecurityGroupId
```

Эти RPC дёргают только сервисы (kacho-vpc сам себя через wiring или
теоретически другие kacho-* через gRPC). Не зарегистрированы в apiGW
restmux.

## Operations (LRO)

Все мутации (Create/Update/Delete/Move/AddCidrBlocks/...) возвращают
`Operation`. Шаблон:

```protobuf
service NetworkService {
  rpc Get (GetNetworkRequest) returns (Network);                     // sync read
  rpc List (...) returns (ListNetworksResponse);                     // sync read
  rpc Create (CreateNetworkRequest) returns (operation.Operation);   // async
  rpc Update (UpdateNetworkRequest) returns (operation.Operation);   // async
  rpc Delete (DeleteNetworkRequest) returns (operation.Operation);   // async
  rpc Move (MoveNetworkRequest) returns (operation.Operation);       // async
}
```

Клиент полит `OperationService.Get(operation_id)` до `done=true` (REST: `GET /operations/{id}`,
**без** `/vpc/v1/` префикса). api-gateway имеет in-process `opsproxy` — один URL
`/operations/{id}` маршрутизируется по 3-char prefix ID на нужный backend
(`enp...` → kacho-vpc; `b1g...` → resource-manager). `PrefixOperationVPC == PrefixNetwork == "enp"`.
Неизвестный prefix → `400 INVALID_ARGUMENT "unknown prefix"` (FINDING-003).

⚠️ **Контракт-нарушение**: все 6 Delete RPC возвращают `DeleteXxxMetadata`
в `response`, а должны — `google.protobuf.Empty` (verbatim YC). См. TODO #1.

## Где смотреть proto

```
kacho-proto/proto/kacho/cloud/vpc/v1/
├── network.proto                       Network message
├── network_service.proto               NetworkService RPC
├── subnet.proto / subnet_service.proto
├── address.proto / address_service.proto
├── route_table.proto / route_table_service.proto
├── security_group.proto / security_group_service.proto
├── gateway.proto / gateway_service.proto
├── privatelink/private_endpoint.proto + _service.proto
│
├── internal_geography_service.proto    Region+Zone admin
├── internal_address_pool_service.proto AddressPool admin + observability
├── internal_cloud_service.proto        CloudPoolSelector admin
├── internal_network_service.proto      SetDefaultSecurityGroupId
├── internal_address_service.proto      Allocate*IP, SetIP
└── internal_watch_service.proto        Watch outbox
```

Generated stubs: `kacho-proto/gen/go/kacho/cloud/vpc/v1/...`. Импорт:

```go
vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
pepb  "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
```
