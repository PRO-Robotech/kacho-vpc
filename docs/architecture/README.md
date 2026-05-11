# kacho-vpc — Architecture

Архитектурная документация именно по VPC-сервису. Workspace-уровень (как
он связан с другими сервисами, общий стек) — в
`kacho-workspace/docs/architecture/`.

> **Итоговый самодостаточный документ** — [`../ARCHITECTURE.md`](../ARCHITECTURE.md).
> Документы ниже — детализация по конкретным темам.

## Содержание

| # | Документ | О чём |
|---|---|---|
| 00 | [Overview](00-overview.md) | Что делает VPC, какие ресурсы owns, его место в общей системе |
| 01 | [Resources](01-resources.md) | Детально по каждому ресурсу: Network, Subnet, Address, RouteTable, SecurityGroup, Gateway, PrivateEndpoint, Region, Zone, AddressPool |
| 02 | [Data Flows](02-data-flows.md) | Sequence-диаграммы VPC-сценариев: Network create + default-SG, Address allocate cascade, Internal alloc, Watch outbox, Cloud-selector set |
| 03 | [IPAM Model](03-ipam.md) | Region/Zone/Pool/CloudSelector + cascade resolve + utilization (главная нетривиальная фича) |
| 04 | [API Surface](04-api-surface.md) | Все RPC (public 59 + internal 29), REST endpoints, верстки путей |
| 05 | [Database](05-database.md) | Схема pg-vpc, миграции (`0001_initial.sql` squashed baseline + `0002_resource_name_unique.sql`), ключевые constraints (EXCLUDE для CIDR, partial UNIQUE, JSONB GIN) |
| 06 | [Conventions & Gotchas](06-conventions.md) | VPC-specific правила, error mapping, top-10 уроков из истории фиксов |
| 07 | [Known divergences](07-known-divergences.md) | Осознанные расхождения с verbatim-YC / спорные решения (by-design — не баги; баги/задачи — в GitHub Issues) |
| 09 | [Go skills applied](09-go-skills-applied.md) | Как применены практики code/test coaching; что закрыто рефакторингами |

## TL;DR — что это за сервис

Один из двух domain-сервисов Kachō (второй — `kacho-resource-manager`).
Owns два слоя:

- **VPC ресурсы (verbatim YC)**: Network, Subnet, Address, RouteTable,
  SecurityGroup, Gateway, PrivateEndpoint. Public API на gRPC `:9090`,
  через api-gateway → REST `/vpc/v1/...`. Folder-scoped (ссылка на
  resource-manager.Folder).
- **IPAM (kacho-only, admin)**: Region, Zone, AddressPool, CloudPoolSelector,
  bindings (network_default, address_override). Internal-only API на
  gRPC `:9091`. Глобальные ресурсы — не привязаны к org/cloud/folder.
  Управляются админом через web-UI / curl-REST на api-gateway internal mux.

Cascade IP-allocate работает inline в worker'е `AddressService.doCreate`
(раньше был отдельный `kacho-vpc-controllers` процесс — выпилен в Phase 2).

## Связь с другими репо

```
       ┌──────────────────────────────────┐
       │       kacho-api-gateway          │
       └─────┬──────────────────┬─────────┘
             │ public :9090     │ admin internal :9091
             ▼                  ▼
       ┌──────────────────────────────────┐
       │           kacho-vpc              │
       │  ┌──────────────────┐            │
       │  │  service layer   │            │
       │  └─┬────────┬───────┘            │
       │    │        │ FolderClient       │
       │    │        └──→ kacho-resource- │
       │    │             manager (gRPC)  │
       │    │             FolderService.Get
       │    │             folder_id → cloud_id
       │    │                              │
       │    ▼                              │
       │  ┌──────────────────┐            │
       │  │  pg-vpc (own DB) │            │
       │  └──────────────────┘            │
       └──────────────────────────────────┘
```

Внешние зависимости:
- `kacho-resource-manager.FolderService.Get` — для existence check
  `folder_id` и для resolve `folder_id → cloud_id` в IPAM cascade.
- `kacho-corelib` — `ids`, `operations`, `db`, `grpcsrv`, `outbox`, etc.
- `kacho-proto` — все .proto, generated stubs.

VPC **не знает** про:
- api-gateway (просто слушает 9090/9091).
- UI/TUI/CLI (это REST/gRPC потребители).
- compute/loadbalancer (frozen).

См. [`02-data-flows.md`](02-data-flows.md#cross-service-folder-cloud-id-lookup).
