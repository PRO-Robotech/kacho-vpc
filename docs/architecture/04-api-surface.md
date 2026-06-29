# 04 — API Surface

Полный список RPC kacho-vpc + соответствующие REST endpoints. Public: 8
доменных сервисов (7 ресурсов + `NetworkInterfaceService`) + internal admin-сервисы.

## Сводка

| Категория | Listener | REST exposed |
|---|---|---|
| Public домены (8: 7 + `NetworkInterfaceService`) | `:9090` (public gRPC) | ✅ да, через api-gateway (оба listener'а) |
| Internal admin (kacho-only) | `:9091` (internal gRPC) | ✅ выборочно — только cluster-internal listener (CRUD + admin actions) |

## Public сервисы (`:9090`)

| Сервис | RPC | Что делает |
|---|---|---|
| `NetworkService` | CRUD + ListSubnets + ListSecurityGroups + ListRouteTables + ListOperations | у `Network` нет внутреннего инфра-идентификатора |
| `SubnetService` | CRUD + AddCidrBlocks + RemoveCidrBlocks + ListUsedAddresses + ListOperations | `v4_cidr_blocks` опционально на Create; `:add/:remove-cidr-blocks` принимают **и `v6_cidr_blocks`** (валидный IPv6-префикс, host-bits=0, intra-request disjoint, overlap → `FailedPrecondition`); `UpdateSubnet` получил `v6_cidr_blocks` (soft-immutable / no-op) |
| `AddressService` | CRUD + GetByValue + ListBySubnet + ListOperations | `CreateAddressRequest` получил `internal_ipv6_address_spec`; `ListAddressesRequest.subnet_id` матчит `internal_ipv4`/`internal_ipv6`; `Delete` адреса в использовании у NIC → `FailedPrecondition` |
| `RouteTableService` | CRUD + AddRoutes + RemoveRoutes + UpdateRoute + ListOperations | |
| `SecurityGroupService` | CRUD + UpdateRules + UpdateRule + ListOperations | `network_id` опционально на Create (project-level / network-less SG); `List?filter=network_id="<id>"` |
| `GatewayService` | CRUD + ListOperations | |
| `NetworkInterfaceService` | Get + List + Create + Update + Delete + ListOperations | REST `/vpc/v1/networkInterfaces`; NIC принадлежит `Subnet` (`subnet_id`), ссылается на `Address` по id (`v4_address_ids[]`/`v6_address_ids[]`), `security_group_ids[]`, `used_by` (денормализованное зеркало — кто использует NIC); проекция чисто control-plane (lean) — инфра-полей у kacho-vpc нет |

> `ListOperations` для Network/Subnet/Address/NetworkInterface не требует существования ресурса
> (precondition `repo.Get` убран — handler best-effort: жив → project-ownership; NotFound → пропуск).
> Для route_table/SG/gateway `ListOperations` по-прежнему гейтит на `repo.Get`.

REST mapping — `google.api.http` аннотации в proto, см. `kacho-proto/proto/kacho/cloud/vpc/v1/<resource>_service.proto`.

## Internal admin сервисы (`:9091`, kacho-only)

| Сервис | RPC | Что делает |
|---|---|---|
| `InternalAddressPoolService` | CRUD пулов + binding (BindAsNetworkDefault / UnbindNetworkDefault) + observability (ListAddresses, GetUtilization) | |
| `InternalNetworkService` | GetNetwork (internal-only `vrf_id`) + SetDefaultSecurityGroupId (admin-only computed-field setter) | публичная проекция `Network` инфра-полей не содержит |
| `InternalAddressService` | AllocateInternalIP / **AllocateInternalIPv6** / AllocateExternalIP + SetAddressReference / ClearAddressReference / GetAddressReference (referrer-tracking «кто использует адрес» — отражается в `Address.used` и `SubnetService.ListUsedAddresses.references[]`; referrer'ы: `compute_instance`, `network_interface`) | |
| ~~`InternalRegionService` / `InternalZoneService`~~ | — | Geography (Region/Zone) живет в leaf-домене `kacho-geo`; в kacho-vpc этих сервисов нет |

## REST endpoints (через api-gateway)

### Public (exposed на оба listener'а)

```
# Network
GET    /vpc/v1/networks?projectId=
POST   /vpc/v1/networks                              → Operation
GET    /vpc/v1/networks/{network_id}
PATCH  /vpc/v1/networks/{network_id}                 → Operation
DELETE /vpc/v1/networks/{network_id}                 → Operation
GET    /vpc/v1/networks/{network_id}/subnets
GET    /vpc/v1/networks/{network_id}/security_groups   # snake_case в child-list!
GET    /vpc/v1/networks/{network_id}/route_tables      # snake_case!
GET    /vpc/v1/networks/{network_id}/operations

# Subnet (analogously)
GET/POST/PATCH/DELETE /vpc/v1/subnets[/{id}]   # v4_cidr_blocks опционально на POST
GET    /vpc/v1/subnets/{subnet_id}/addresses         (UsedAddress[])
GET    /vpc/v1/subnets/{subnet_id}/operations        # переживает удаление подсети
POST   /vpc/v1/subnets/{subnet_id}:add-cidr-blocks   # body: {v4CidrBlocks?, v6CidrBlocks?} — теперь и v6
POST   /vpc/v1/subnets/{subnet_id}:remove-cidr-blocks # body: {v4CidrBlocks?, v6CidrBlocks?}

# Address
GET/POST/PATCH/DELETE /vpc/v1/addresses[/{id}]   # POST принимает internalIpv6AddressSpec
GET    /vpc/v1/addresses:byValue?value=<ip>
GET    /vpc/v1/addresses:bySubnet?subnetId=<id>  # ListBySubnet
GET    /vpc/v1/addresses?subnetId=<id>           # фильтр по internal_ipv4 ИЛИ internal_ipv6

# NetworkInterface (top-level camelCase networkInterfaces)
GET/POST/PATCH/DELETE /vpc/v1/networkInterfaces[/{id}]   # POST: subnet_id; v4_address_ids/v6_address_ids/security_group_ids опциональны
GET    /vpc/v1/networkInterfaces/{network_interface_id}/operations   # переживает удаление NIC

# RouteTable (top-level — camelCase routeTables)
GET/POST/PATCH/DELETE /vpc/v1/routeTables[/{id}]

# SecurityGroup
GET/POST/PATCH/DELETE /vpc/v1/securityGroups[/{id}]   # POST: network_id опционален; GET?filter=network_id="<id>"
PATCH  /vpc/v1/securityGroups/{sg_id}/rules           # UpdateRules — PATCH на /rules
PATCH  /vpc/v1/securityGroups/{sg_id}/rules/{rule_id} # UpdateRule

# Gateway
GET/POST/PATCH/DELETE /vpc/v1/gateways[/{id}]
```

> ⚠️ REST-пути неоднородны (наследие proto-аннотаций, proto-decided; см.
> `docs/architecture/07-known-divergences.md`): child-list `security_groups`/`route_tables` —
> snake_case, top-level `routeTables`/`securityGroups`/`addressPools` — camelCase,
> custom-методы — kebab с двоеточием (`:add-cidr-blocks`),
> `OperationService.Get` — `/operations/{id}` (без `/vpc/v1/`).

### Admin (kacho-only, **только cluster-internal listener**)

```
# (Region/Zone admin — домен kacho-geo: /geo/v1/{regions,zones}; в kacho-vpc их нет)

# AddressPool
GET    /vpc/v1/addressPools?zoneId=&kind=
POST   /vpc/v1/addressPools
GET/PATCH/DELETE /vpc/v1/addressPools/{pool_id}

# AddressPool admin actions
GET    /vpc/v1/addressPools/{pool_id}/utilization
GET    /vpc/v1/addressPools/{pool_id}/addresses?projectId=

# AddressPool binding
POST   /vpc/v1/networks/{network_id}/addressPoolBinding   {poolId}
DELETE /vpc/v1/networks/{network_id}/addressPoolBinding
```

⚠️ Все admin paths **не должны** быть доступны на external TLS endpoint
(`api.kacho.local:443`, advertised для внешних клиентов). См. [`06-conventions.md`](06-conventions.md#admin-boundary).

### Internal-only (НЕ через apiGW REST, gRPC server-to-server)

```
InternalAddressService.AllocateInternalIP / AllocateInternalIPv6 / AllocateExternalIP / SetAddressReference / ClearAddressReference / GetAddressReference
InternalNetworkService.GetNetwork / SetDefaultSecurityGroupId
```

Эти RPC дергают только сервисы (kacho-vpc сам себя через wiring или
теоретически другие kacho-* через gRPC). Не зарегистрированы в apiGW
restmux.

## Operations (LRO)

Все мутации (Create/Update/Delete/AddCidrBlocks/...) возвращают
`Operation`. Шаблон:

```protobuf
service NetworkService {
  rpc Get (GetNetworkRequest) returns (Network);                     // sync read
  rpc List (...) returns (ListNetworksResponse);                     // sync read
  rpc Create (CreateNetworkRequest) returns (operation.Operation);   // async
  rpc Update (UpdateNetworkRequest) returns (operation.Operation);   // async
  rpc Delete (DeleteNetworkRequest) returns (operation.Operation);   // async
}
```

Клиент полит `OperationService.Get(operation_id)` до `done=true` (REST: `GET /operations/{id}`,
**без** `/vpc/v1/` префикса). api-gateway имеет in-process `opsproxy` — один URL
`/operations/{id}` маршрутизируется по 3-char prefix ID на нужный backend
(`enp...` → kacho-vpc). Operation.id несет отдельный per-domain prefix
`PrefixOperationVPC = "enp"` (декаплен от ресурсных prefix'ов вроде `net`).
Неизвестный prefix → `400 INVALID_ARGUMENT "unknown prefix"` (intentional fail-fast
перед роутингом; см. `docs/architecture/07-known-divergences.md`).

Все 6 Delete RPC возвращают `google.protobuf.Empty` в `response`;
`DeleteXxxMetadata` лежит в `Operation.metadata`, как и положено по proto-options.

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
├── network_interface.proto / network_interface_service.proto   NetworkInterface
│
├── internal_address_pool_service.proto AddressPool admin + observability
├── internal_network_service.proto      GetNetwork (internal-only vrf_id) + SetDefaultSecurityGroupId
└── internal_address_service.proto      Allocate*IP (v4/v6/ext), {Set,Clear,Get}AddressReference
# (Region/Zone — домен kacho-geo: proto/kacho/cloud/geo/v1/)
```

Generated stubs: `kacho-proto/gen/go/kacho/cloud/vpc/v1/...`. Импорт:

```go
vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
```
