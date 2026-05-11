# 00 — Overview

## Роль сервиса

`kacho-vpc` — один из двух доменных сервисов Kachō (control-plane only;
real data-plane живёт отдельным проектом `kacho-vpc-implement` как spec).
Самый объёмный сервис в системе.

Owns:
- 7 публичных VPC-ресурсов (verbatim YC API).
- 4 IPAM-ресурса (kacho-only, admin).
- 3 binding-таблицы (admin connectors между ресурсами).
- inline IPAM allocation (раньше в отдельном процессе, теперь в
  request-path).
- inline default-SG creation.
- in-process outbox + LISTEN/NOTIFY для подписки на изменения.

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
                  │   InternalAddressService (allocate IP)        │
                  └───────────────────────────────────────────────┘
```

## Ресурсы — две группы

**Клиентская (verbatim YC, folder-scoped)** — то что видит конечный клиент:

| Ресурс | Назначение | ID prefix |
|---|---|---|
| Network | VPC-сеть | `enp` |
| Subnet | подсеть в Network, привязана к Zone | `e9b` |
| Address | external (publicIP) или internal (IP в Subnet) | `e9b` |
| RouteTable | static routes для Network | `enp` |
| SecurityGroup | firewall rules, привязан к Network | `enp` |
| Gateway | shared egress (NAT-style) | `enp` |
| PrivateEndpoint | privatelink connection | `enp` |

> Префиксы — из `kacho-corelib/ids`. `Network/RouteTable/SecurityGroup/Gateway/
> PrivateEndpoint` делят `enp` (api-gateway маршрутизирует `OperationService.Get`
> по первым 3 символам id; для VPC-домена это `enp`). `Subnet/Address` — `e9b`.
> `PrefixOperationVPC == PrefixNetwork == "enp"`.

**Системная (kacho-only, admin, глобальная)** — то что админ управляет
для обеспечения IP allocation:

| Ресурс | Назначение | ID format |
|---|---|---|
| Region | географический регион | строка `ru-central1` |
| Zone | зона в регионе | строка `ru-central1-a` |
| AddressPool | пул external IP с CIDR-блоками | `apl` |
| CloudPoolSelector | label-привязка Cloud к pool routing | PK = cloud_id |

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
                            GatewayService, PrivateEndpointService.
                            AddressPoolService (admin CRUD + cascade resolve).
                            AddressAllocator (pure IP picker + retry).
                            RegionService, ZoneService.
                            NetworkInternal (computed-field setter).

                          Port-интерфейсы:
                            NetworkRepo, SubnetRepo, AddressRepo, …
                            AddressPoolRepo, AddressPoolBindingRepo,
                            CloudPoolSelectorRepo, RegionRepo, ZoneRepo.
                            FolderClient (cross-service).

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
