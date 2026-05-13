# 00 — Overview

## Роль сервиса

`kacho-vpc` — один из двух доменных сервисов Kachō (control-plane only;
real data-plane живёт отдельным проектом `kacho-vpc-implement` как spec).
Самый объёмный сервис в системе.

Owns:
- 8 публичных VPC-ресурсов: 7 исторических (Network, Subnet, Address, RouteTable,
  SecurityGroup, Gateway, PrivateEndpoint) + `NetworkInterface` (first-class AWS-ENI-подобный
  ресурс, эпик KAC-2) — а также инфра-поле `vpn_id` на `Network` (24-bit data-plane-id, internal-only).
- AddressPool + CloudPoolSelector + binding-таблицы (kacho-only, admin).
  (Region/Zone — перенесены в `kacho-compute`, эпик KAC-15.)
- inline IPAM allocation (раньше в отдельном процессе, теперь в
  request-path) — internal/external IPv4 + internal IPv6.
- inline default-SG creation + аллокация `vpn_id` на Network.Create.
- in-process outbox + LISTEN/NOTIFY для подписки на изменения.
- `InternalNetworkInterfaceService` — internal-проекция NIC (data-plane-инфа) +
  write-back `ReportNiDataplane` от `kacho-vpc-implement`.

## Что делает (логически)

```
                  ┌───────────────────────────────────────────────┐
                  │                  kacho-vpc                    │
                  │                                               │
     public  ──►  │   verbatim-YC API (7 ресурсов)                │
                  │   ├─ Network, Subnet, Address                 │
                  │   ├─ RouteTable, SecurityGroup                │
                  │   └─ Gateway, PrivateEndpoint                 │
                  │                                               │
     admin   ──►  │   kacho-only API (4 ресурса + 3 binding)      │
                  │   ├─ Region, Zone (глобальный admin)          │
                  │   ├─ AddressPool (глобальный admin)           │
                  │   ├─ CloudPoolSelector (admin → Cloud)        │
                  │   └─ bindings: network/default,               │
                  │                address/override               │
                  │                                               │
     internal ──► │   InternalWatchService (outbox stream)        │
                  │   InternalAddressService (allocate v4/v6/ext) │
                  │   InternalNetworkService (Network + vpn_id)   │
                  │   InternalNetworkInterfaceService             │
                  │     (NIC data-plane proj + ReportNiDataplane) │
                  └───────────────────────────────────────────────┘
```

> Region/Zone admin (`InternalRegionService`/`InternalZoneService`) — **удалены** из kacho-vpc:
> Geography переехала в `kacho-compute` (эпик KAC-15; миграция `0004_drop_geography.sql`).

## Ресурсы — две группы

**Клиентская (verbatim YC, folder-scoped)** — то что видит конечный клиент:

| Ресурс | Назначение | ID prefix |
|---|---|---|
| Network | VPC-сеть (+ internal-only `vpn_id` 24-bit) | `enp` |
| Subnet | подсеть в Network (zone — id-строка домена compute); `v4_cidr_blocks` опционально на Create | `e9b` |
| Address | external (publicIP) или internal (IPv4/IPv6 в Subnet) | `e9b` |
| NetworkInterface | first-class NIC (эпик KAC-2): принадлежит Subnet, ссылается на Address по id | `e9b` (переиспользует `PrefixSubnet`) |
| RouteTable | static routes для Network | `enp` |
| SecurityGroup | firewall rules; `network_id` опционально на Create (folder-level SG) | `enp` |
| Gateway | shared egress (NAT-style) | `enp` |
| PrivateEndpoint | privatelink connection | `enp` |

> Префиксы — из `kacho-corelib/ids`. `Network/RouteTable/SecurityGroup/Gateway/
> PrivateEndpoint` делят `enp` (api-gateway маршрутизирует `OperationService.Get`
> по первым 3 символам id; для VPC-домена это `enp`). `Subnet/Address/NetworkInterface` — `e9b`
> (NIC переиспользует `PrefixSubnet`, отдельного `PrefixNetworkInterface` нет).
> `PrefixOperationVPC == PrefixNetwork == "enp"`.

**Системная (kacho-only, admin, глобальная)** — то что админ управляет
для обеспечения IP allocation:

| Ресурс | Назначение | ID format |
|---|---|---|
| AddressPool | пул external IP с CIDR-блоками | `apl` |
| CloudPoolSelector | label-привязка Cloud к pool routing | PK = cloud_id |

> Region/Zone (`ru-central1` / `ru-central1-a`) — больше **не в kacho-vpc**, а в `kacho-compute`
> (эпик KAC-15). `subnet.zone_id` / `address_pool.zone_id` хранятся как `TEXT`-id без FK,
> валидируются через `compute.v1.ZoneService.Get`.

**Bindings** (внутренние таблицы для cascade resolve):

| Binding | PK | Связывает |
|---|---|---|
| `address_pool_network_default` | network_id | Network → AddressPool (override на zone-default) |
| `address_pool_address_override` | address_id | конкретный Address → AddressPool |

## Layered architecture

Стандартная Clean Architecture:

```
cmd/vpc/main.go           composition root: pgxpool, repo'ы, services,
                          handlers, two gRPC servers (9090 + 9091).

internal/
  domain/                 pure Go structs, без зависимостей. Network,
                          Subnet, Address, AddressPool, Region, Zone,
                          CloudPoolSelector, …

  service/                use-cases:
                            NetworkService, SubnetService, AddressService,
                            RouteTableService, SecurityGroupService,
                            GatewayService, PrivateEndpointService,
                            NetworkInterfaceService (эпик KAC-2 — CRUD + Attach/Detach).
                            AddressPoolService (admin CRUD + cascade resolve).
                            AddressAllocator (pure IP picker + retry; v4/v6/ext).
                            NetworkInternal (vpn_id + computed-field setter).
                            NetworkInterfaceInternal (data-plane проекция + ReportNiDataplane).

                          Port-интерфейсы:
                            NetworkRepo, SubnetRepo, AddressRepo, NetworkInterfaceRepo, …
                            AddressPoolRepo, AddressPoolBindingRepo,
                            CloudPoolSelectorRepo, VpnIDAllocator.
                            FolderClient, GeographyRegistry (compute.v1.ZoneService — cross-service).

  repo/                   pgx adapter, реализация ports + outbox emit.
                          Один файл на ресурс.

  clients/                gRPC adapter — FolderClient (resourcemanager
                          gRPC stub).

  handler/                gRPC server-сторона. Тонкие, делегируют в service.
                          Public-сервисы и Internal-сервисы — отдельные
                          handler-файлы, в одной server-инстанции по портам.

  migrations/             *.sql, embed.FS, goose-стиль up/down.
```

## Зависимости

**Inbound** (кто дёргает kacho-vpc):
- `kacho-api-gateway` — proxy для REST/gRPC клиентов.
- admin-tooling (curl/REST через api-gateway internal mux) / web-UI на :9091 RPC.
- (потенциально) `kacho-compute`, `kacho-loadbalancer` — frozen.

**Outbound** (кого дёргает kacho-vpc):
- `kacho-resource-manager.FolderService.Get` — единственная межсервисная
  зависимость. Нужна:
  - existence check `folder_id` в Create мутациях (verbatim YC error
    `"Folder with id X not found"`);
  - resolve `folder_id → cloud_id` в IPAM cascade Step 3 (cloud-selector
    lookup для external Address).

Никаких других кросс-сервисных вызовов нет.

## База данных

`kacho_vpc` (`pg-vpc` StatefulSet в helm umbrella). Database-per-service —
никаких JOIN'ов с rm-БД или внешними источниками.

Особенности:
- Миграции в `internal/migrations/*.sql` (embed.FS) — `0001_initial.sql`
  (squashed baseline, 22 исторические миграции в одном файле) +
  `0002_resource_name_unique.sql` (partial UNIQUE `(folder_id, name)`).
- Используем продвинутые Postgres-фичи: `EXCLUDE USING gist` (CIDR
  no-overlap), partial UNIQUE indices, computed columns, `inet/cidr`
  типы и операторы (`<<`, `>>=`), `JSONB` containment с GIN индексом
  (`jsonb_path_ops`), `LISTEN/NOTIFY` для outbox stream, `xmin::text` для
  optimistic locking.

См. [`05-database.md`](05-database.md).

## Что НЕ owns kacho-vpc

- Org/Cloud/Folder — это `kacho-resource-manager`. VPC только проверяет
  через FolderClient.
- Operations storage — `operations` таблица копируется из corelib через
  `make sync-migrations`, но логика worker'а в `kacho-corelib/operations`.
- Реальный data-plane (фактическое forwarding пакетов) — это другой
  проект (`kacho-vpc-implement`, spec-only).
- Compute/instances/disks — `kacho-compute` (frozen).

## Quick links

- [Resources детально](01-resources.md)
- [Data flows / sequence](02-data-flows.md)
- [IPAM (главное)](03-ipam.md)
- [API surface (RPC список)](04-api-surface.md)
- [DB schema + миграции](05-database.md)
- [Conventions + gotchas](06-conventions.md)

Дополнительно:
- `../../CLAUDE.md` — operational правила для AI agents (компактнее).
- GitHub Issues (`github.com/PRO-Robotech/kacho-vpc/issues`) — долги, баги, planned issues.
- [07-known-divergences.md](07-known-divergences.md) — registry by-design расхождений с verbatim YC.
- `../../tests/newman/` — e2e regression suite.
