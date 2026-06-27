# 00 — Overview

## Роль сервиса

`kacho-vpc` — один из двух доменных сервисов Kachō (control-plane only).
Самый объемный сервис в системе.

Owns:
- 7 публичных VPC-ресурсов: Network, Subnet, Address, RouteTable,
  SecurityGroup, Gateway + `NetworkInterface` (first-class NIC-ресурс домена).
  У `Network` есть internal-only инфра-идентификатор (`vrf_id`), не на публичной поверхности.
- AddressPool + network-default binding-таблица (kacho-only, admin).
  (Region/Zone — leaf-домен `kacho-geo`, в kacho-vpc только `zone_id`-ссылка без FK.)
- inline IPAM allocation в request-path — internal/external IPv4 + internal IPv6.
- inline default-SG creation на Network.Create.
- in-process outbox + LISTEN/NOTIFY для подписки на изменения.

## Что делает (логически)

```
                  ┌───────────────────────────────────────────────┐
                  │                  kacho-vpc                    │
                  │                                               │
     public  ──►  │   публичный API (6 ресурсов)                  │
                  │   ├─ Network, Subnet, Address                 │
                  │   ├─ RouteTable, SecurityGroup                │
                  │   └─ Gateway                                  │
                  │                                               │
     admin   ──►  │   kacho-only API (AddressPool + binding)      │
                  │   ├─ AddressPool (глобальный admin)           │
                  │   └─ binding: network/default                 │
                  │                                               │
     internal ──► │   InternalAddressService (allocate v4/v6/ext) │
                  │   InternalNetworkService (vrf_id / default-SG)│
                  │   InternalAddressPoolService (admin пулы)     │
                  └───────────────────────────────────────────────┘
```

> Region/Zone admin (`InternalRegionService`/`InternalZoneService`) — **не в kacho-vpc**:
> Geography — отдельный leaf-домен `kacho-geo`. VPC только ссылается на `zone_id` (TEXT, без FK).

## Ресурсы — две группы

**Клиентская (project-scoped)** — то что видит конечный клиент:

| Ресурс | Назначение | ID prefix |
|---|---|---|
| Network | VPC-сеть | `net` |
| Subnet | подсеть в Network (zone — id-строка домена geo); `v4_cidr_blocks` опционально на Create | `sub` |
| Address | external (publicIP) или internal (IPv4/IPv6 в Subnet) | `adr` |
| NetworkInterface | first-class NIC: принадлежит Subnet, ссылается на Address по id | `nic` |
| RouteTable | static routes для Network | `rtb` |
| SecurityGroup | firewall rules; `network_id` опционально на Create (project-level SG) | `sgr` |
| Gateway | shared egress (NAT-style) | `gtw` |

> Префиксы — из `kacho-corelib/ids` (3-char + 17 crockford-base32), у каждого ресурса свой.
> Operation у VPC несет **отдельный** prefix `PrefixOperationVPC = "enp"` (декаплен от
> ресурсных prefix'ов): api-gateway маршрутизирует `OperationService.Get` по первым 3
> символам id на нужный backend.

**Системная (kacho-only, admin, глобальная)** — то что админ управляет
для обеспечения IP allocation:

| Ресурс | Назначение | ID format |
|---|---|---|
| AddressPool | пул external IP с CIDR-блоками | `apl` |

> Region/Zone (`zone` / `zone-a`) — **не в kacho-vpc**, а в leaf-домене
> `kacho-geo`. `subnet.zone_id` / `address_pool.zone_id` хранятся как `TEXT`-id без FK,
> валидируются через `geo.v1.ZoneService.Get`.

**Bindings** (внутренние таблицы для cascade resolve):

| Binding | PK | Связывает |
|---|---|---|
| `address_pool_network_default` | network_id | Network → AddressPool (override на zone-default) |

## Layered architecture

Стандартная Clean Architecture:

```
cmd/vpc/main.go           composition root: pgxpool, repo'ы, services,
                          handlers, two gRPC servers (9090 + 9091).

internal/
  domain/                 pure Go structs, без зависимостей. Network,
                          Subnet, Address, AddressPool, …

  service/                use-cases:
                            NetworkService, SubnetService, AddressService,
                            RouteTableService, SecurityGroupService,
                            GatewayService,
                            NetworkInterfaceService (CRUD).
                            AddressPoolService (admin CRUD + cascade resolve).
                            AddressAllocator (pure IP picker + retry; v4/v6/ext).
                            NetworkInternal (admin default-SG setter).

                          Port-интерфейсы:
                            NetworkRepo, SubnetRepo, AddressRepo, NetworkInterfaceRepo, …
                            AddressPoolRepo, AddressPoolBindingRepo.
                            ProjectClient, ZoneRegistry (geo.v1.ZoneService — cross-service).

  repo/                   pgx adapter, реализация ports + outbox emit.
                          Один файл на ресурс.

  clients/                gRPC adapter — ProjectClient (kacho-iam
                          gRPC stub).

  handler/                gRPC server-сторона. Тонкие, делегируют в service.
                          Public-сервисы и Internal-сервисы — отдельные
                          handler-файлы, в одной server-инстанции по портам.

  migrations/             *.sql, embed.FS, goose-стиль up/down.
```

## Зависимости

**Inbound** (кто дергает kacho-vpc):
- `kacho-api-gateway` — proxy для REST/gRPC клиентов.
- admin-tooling (curl/REST через api-gateway internal mux) / web-UI на :9091 RPC.
- `kacho-compute` — валидация NIC-spec (Subnet/SecurityGroup) + IPAM-аллокация Address.

**Outbound** (кого дергает kacho-vpc):
- `kacho-iam.ProjectService.Get` — existence check владельца-проекта
  (`project_id` — id владельца-проекта) в Create-мутациях (канонический error
  `"Project X not found"`); `InternalIAMService.Check` — per-RPC authz-gate;
  `RegisterResource`/`UnregisterResource` — запись owner-tuple в FGA через IAM.
- `kacho-geo.ZoneService.Get` — валидация `zone_id` Subnet/AddressPool на request-path.

## База данных

`kacho_vpc` (`pg-vpc` StatefulSet в helm umbrella). Database-per-service —
никаких JOIN'ов с rm-БД или внешними источниками.

Особенности:
- Миграции в `internal/migrations/*.sql` (embed.FS) — `0001_initial.sql`
  (baseline-схема со всеми таблицами/индексами/constraints) + инкрементные `0002`+
  (см. [`05-database.md`](05-database.md)).
- Используем продвинутые Postgres-фичи: `EXCLUDE USING gist` (CIDR
  no-overlap), partial UNIQUE indices, computed columns, `inet/cidr`
  типы и операторы (`<<`, `>>=`), `JSONB` containment с GIN индексом
  (`jsonb_path_ops`), `LISTEN/NOTIFY` для outbox stream, `xmin::text` для
  optimistic locking.

См. [`05-database.md`](05-database.md).

## Что НЕ owns kacho-vpc

- Account/Project — это `kacho-iam`. VPC только проверяет существование
  владельца-проекта через ProjectClient.
- Region/Zone — это `kacho-geo` (leaf-домен Geography). VPC ссылается на `zone_id`
  по TEXT-id без FK, валидирует через `geo.v1.ZoneService.Get`.
- Operations storage — `operations` таблица из corelib (`make sync-migrations`),
  логика worker'а — в `kacho-corelib/operations`.
- Compute/instances/disks — `kacho-compute`.

## Quick links

- [Resources детально](01-resources.md)
- [Data flows / sequence](02-data-flows.md)
- [IPAM (главное)](03-ipam.md)
- [API surface (RPC список)](04-api-surface.md)
- [DB schema + миграции](05-database.md)
- [Conventions + gotchas](06-conventions.md)

Дополнительно:
- GitHub Issues (`github.com/PRO-Robotech/kacho-vpc/issues`) — долги, баги, planned issues.
- [07-known-divergences.md](07-known-divergences.md) — реестр намеренных дизайн-решений Kachō VPC.
- `tests/newman/` — e2e regression suite.
