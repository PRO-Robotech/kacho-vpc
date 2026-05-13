# 04 — API Surface

Полный список RPC kacho-vpc + соответствующие REST endpoints. Public: 8 verbatim-исторических
доменных сервисов (7 + `NetworkInterfaceService`, эпик KAC-2) + internal kacho-only сервисы.

## Сводка

| Категория | Listener | REST exposed |
|---|---|---|
| Public домены (8: 7 + `NetworkInterfaceService`) | `:9090` (public gRPC) | ✅ да, через api-gateway (оба listener'а) |
| Internal admin / data-plane (kacho-only) | `:9091` (internal gRPC) | ✅ выборочно — только cluster-internal listener (CRUD + admin actions + NIC/Network internal-проекции + `ReportNiDataplane`/`ListByHypervisor`) |
| Outbox stream (`InternalWatchService`) | `:9091` | ❌ только server-to-server |

## Public сервисы (`:9090`, verbatim-исторические)

| Сервис | RPC | Что делает |
|---|---|---|
| `NetworkService` | CRUD + Move + ListSubnets + ListSecurityGroups + ListRouteTables + ListOperations | (`vpn_id` не в публичном `Network` — см. `InternalNetworkService` ниже) |
| `SubnetService` | CRUD + Move + AddCidrBlocks + RemoveCidrBlocks + Relocate + ListUsedAddresses + ListOperations | `v4_cidr_blocks` опционально на Create; `:add/:remove-cidr-blocks` принимают **и `v6_cidr_blocks`** (валидный IPv6-префикс, host-bits=0, intra-request disjoint, overlap → `FailedPrecondition`); `UpdateSubnet` получил `v6_cidr_blocks` (soft-immutable / no-op) |
| `AddressService` | CRUD + Move + GetByValue + ListOperations | `CreateAddressRequest` получил `internal_ipv6_address_spec`; `ListAddressesRequest.subnet_id` матчит `internal_ipv4`/`internal_ipv6`; `Delete` адреса в использовании у NIC → `FailedPrecondition` |
| `RouteTableService` | CRUD + Move + ListOperations | |
| `SecurityGroupService` | CRUD + Move + UpdateRules + UpdateRule + ListOperations | `network_id` опционально на Create (folder-level / network-less SG); `List?filter=network_id="<id>"` |
| `GatewayService` | CRUD + Move + ListOperations | |
| `PrivateEndpointService` | CRUD + Move | |
| `NetworkInterfaceService` (эпик KAC-2) | Get + List + Create + Update + Delete + AttachToInstance + DetachFromInstance + ListOperations | REST `/vpc/v1/networkInterfaces`; NIC принадлежит `Subnet` (`subnet_id`), ссылается на `Address` по id (`v4_address_ids[]`/`v6_address_ids[]`), `security_group_ids[]`, `used_by` (выставляет Attach, чистит Detach); публичная проекция lean (data-plane-инфа — в `InternalNetworkInterfaceService`) |

> `ListOperations` для Network/Subnet/Address/NetworkInterface не требует существования ресурса
> (precondition `repo.Get` убран — handler best-effort: жив → folder-ownership; NotFound → пропуск).
> Для route_table/SG/gateway/private_endpoint `ListOperations` по-прежнему гейтит на `repo.Get`.

REST mapping — `google.api.http` аннотации в proto, см. `kacho-proto/proto/kacho/cloud/vpc/v1/<resource>_service.proto`.

## Internal admin / data-plane сервисы (`:9091`, kacho-only)

| Сервис | RPC | Что делает |
|---|---|---|
| `InternalAddressPoolService` | CRUD пулов + bindings (network/address override) + diagnostics (Check, ExplainResolution) + observability (ListAddresses, GetUtilization) | |
| `InternalCloudService` | SetPoolSelector / Unset / Get на Cloud | |
| `InternalNetworkService` | SetDefaultSecurityGroupId (computed-field setter) + **GetNetwork → `InternalNetwork{network, vpn_id}`** | REST `GET /vpc/v1/networks/{network_id}/internal` (internal mux only) — отдаёт публичный `Network` + internal `vpn_id` (24-bit data-plane-id) |
| `InternalAddressService` | AllocateInternalIP / **AllocateInternalIPv6** / AllocateExternalIP + SetAddressReference / ClearAddressReference / GetAddressReference (referrer-tracking «кто использует адрес» — отражается в `Address.used` и `SubnetService.ListUsedAddresses.references[]`; referrer'ы: `compute_instance`, `network_interface`) | |
| `InternalNetworkInterfaceService` (эпик KAC-2) | `GetNetworkInterface` → `InternalNetworkInterface` (lean public NIC + data-plane: resolved `vpn_id`, `hv_id` placement, `sid`/`sid_seq`, `host_iface`, `netns`, `gateway_ip`, `container_id`, `status_error`, `dataplane_revision`, resolved v4/v6 address strings) + `ListByHypervisor` + `ReportNiDataplane` (write-back data-plane-state от `kacho-vpc-implement`) | REST на internal mux: `GET /vpc/v1/networkInterfaces/{network_interface_id}/internal`; `ListByHypervisor` / `ReportNiDataplane` — gRPC-style routes (internal-only) |
| `InternalWatchService` | Watch outbox stream | server-to-server only |
| ~~`InternalRegionService` / `InternalZoneService`~~ | — | удалены из kacho-vpc — Geography (Region/Zone) → `kacho-compute` (эпик KAC-15; миграция 0004 `0004_drop_geography.sql` дропнула таблицы) |

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
GET/POST/PATCH/DELETE /vpc/v1/subnets[/{id}]   # v4_cidr_blocks опционально на POST
GET    /vpc/v1/subnets/{subnet_id}/addresses         (UsedAddress[])
GET    /vpc/v1/subnets/{subnet_id}/operations        # переживает удаление подсети
POST   /vpc/v1/subnets/{subnet_id}:add-cidr-blocks   # body: {v4CidrBlocks?, v6CidrBlocks?} — теперь и v6
POST   /vpc/v1/subnets/{subnet_id}:remove-cidr-blocks # body: {v4CidrBlocks?, v6CidrBlocks?}
POST   /vpc/v1/subnets/{subnet_id}:relocate
POST   /vpc/v1/subnets/{subnet_id}:move

# Address
GET/POST/PATCH/DELETE /vpc/v1/addresses[/{id}]   # POST принимает internalIpv6AddressSpec
GET    /vpc/v1/addresses:byValue?value=<ip>
GET    /vpc/v1/addresses?subnetId=<id>           # фильтр по internal_ipv4 ИЛИ internal_ipv6
POST   /vpc/v1/addresses/{address_id}:move

# NetworkInterface (эпик KAC-2; top-level camelCase networkInterfaces)
GET/POST/PATCH/DELETE /vpc/v1/networkInterfaces[/{id}]   # POST: subnet_id; v4_address_ids/v6_address_ids/security_group_ids опциональны
GET    /vpc/v1/networkInterfaces/{network_interface_id}/operations   # переживает удаление NIC
POST   /vpc/v1/networkInterfaces/{network_interface_id}:attachToInstance
POST   /vpc/v1/networkInterfaces/{network_interface_id}:detachFromInstance

# RouteTable (top-level — camelCase routeTables)
GET/POST/PATCH/DELETE /vpc/v1/routeTables[/{id}]

# SecurityGroup
GET/POST/PATCH/DELETE /vpc/v1/securityGroups[/{id}]   # POST: network_id опционален; GET?filter=network_id="<id>"
PATCH  /vpc/v1/securityGroups/{sg_id}/rules           # UpdateRules — PATCH на /rules
PATCH  /vpc/v1/securityGroups/{sg_id}/rules/{rule_id} # UpdateRule

# Gateway
GET/POST/PATCH/DELETE /vpc/v1/gateways[/{id}]

# PrivateEndpoint — путь /endpoints, НЕ /privateEndpoints!
GET/POST/PATCH/DELETE /vpc/v1/endpoints[/{id}]
GET    /vpc/v1/endpoints/{private_endpoint_id}/operations
```

> ⚠️ REST-пути неоднородны (наследие proto-аннотаций, proto-decided; см.
> `docs/architecture/07-known-divergences.md`): child-list `security_groups`/`route_tables` —
> snake_case, top-level `routeTables`/`securityGroups`/`addressPools` — camelCase,
> custom-методы — kebab с двоеточием (`:add-cidr-blocks`, `:move`),
> `OperationService.Get` — `/operations/{id}` (без `/vpc/v1/`), PE — `/endpoints`.

### Admin / data-plane (kacho-only, **только cluster-internal listener**)

```
# Network internal-проекция (InternalNetworkService.GetNetwork) — содержит vpn_id
GET    /vpc/v1/networks/{network_id}/internal

# NetworkInterface internal-проекция (InternalNetworkInterfaceService.GetNetworkInterface)
GET    /vpc/v1/networkInterfaces/{network_interface_id}/internal
#   + InternalNetworkInterfaceService.ListByHypervisor / ReportNiDataplane — gRPC-style routes
#     (write-back data-plane-state от kacho-vpc-implement; internal-only)

# (Region/Zone admin — переехали в kacho-compute: /compute/v1/{regions,zones}; в kacho-vpc их нет)

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
InternalAddressService.AllocateInternalIP / AllocateInternalIPv6 / AllocateExternalIP / SetAddressReference / ClearAddressReference / GetAddressReference
InternalWatchService.Watch
InternalNetworkService.SetDefaultSecurityGroupId
InternalNetworkInterfaceService.ListByHypervisor / ReportNiDataplane    # kacho-vpc-implement → kacho-vpc write-back
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
Неизвестный prefix → `400 INVALID_ARGUMENT "unknown prefix"` (intentional fail-fast
перед роутингом; см. `docs/architecture/07-known-divergences.md`).

Все 6 Delete RPC возвращают `google.protobuf.Empty` в `response` (verbatim YC);
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
├── privatelink/private_endpoint.proto + _service.proto
├── network_interface.proto / network_interface_service.proto   NetworkInterface (эпик KAC-2)
│
├── internal_address_pool_service.proto AddressPool admin + observability
├── internal_cloud_service.proto        CloudPoolSelector admin
├── internal_network_service.proto      SetDefaultSecurityGroupId + GetNetwork (vpn_id)
├── internal_network_interface_service.proto  GetNetworkInterface + ListByHypervisor + ReportNiDataplane
├── internal_address_service.proto      Allocate*IP (v4/v6/ext), {Set,Clear,Get}AddressReference
└── internal_watch_service.proto        Watch outbox
# (internal_geography_service.proto — Region/Zone — переехал в kacho-compute, эпик KAC-15)
```

Generated stubs: `kacho-proto/gen/go/kacho/cloud/vpc/v1/...`. Импорт:

```go
vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
pepb  "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
```
