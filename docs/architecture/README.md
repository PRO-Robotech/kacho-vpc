# kacho-vpc — Architecture

Архитектурная документация по VPC-сервису.

> **Итоговый самодостаточный документ** — [`../ARCHITECTURE.md`](../ARCHITECTURE.md).
> Документы ниже — детализация по конкретным темам.

## Содержание

| # | Документ | О чем |
|---|---|---|
| 00 | [Overview](00-overview.md) | Что делает VPC, какие ресурсы owns, его место в общей системе |
| 01 | [Resources](01-resources.md) | Детально по каждому ресурсу: Network, Subnet, Address (v4/v6), NetworkInterface, RouteTable, SecurityGroup, Gateway, AddressPool |
| 02 | [Data Flows](02-data-flows.md) | Sequence-диаграммы VPC-сценариев: Network create + default-SG, Address allocate cascade, Internal alloc (v4/v6), outbox-журнал + polling-наблюдение, Cloud-selector set, NIC create/attach/detach, delete-blocking chain |
| 03 | [IPAM Model](03-ipam.md) | Pool + cascade resolve + internal v4/v6 allocate + utilization (Region/Zone — домен kacho-geo) |
| 04 | [API Surface](04-api-surface.md) | Все RPC (public домены + internal kacho-only, в т.ч. `NetworkInterfaceService` + Internal* internal-проекции), REST endpoints, верстки путей |
| 05 | [Database](05-database.md) | Схема pg-vpc, миграции, ключевые constraints (EXCLUDE для CIDR, partial UNIQUE, generated col, JSONB GIN) |
| 06 | [Conventions & Gotchas](06-conventions.md) | VPC-specific правила, error mapping, уроки из истории фиксов |
| 07 | [Намеренные дизайн-решения](07-known-divergences.md) | Осознанные поведенческие решения, которые могут удивить ревьюера (не баги; баги/задачи — в GitHub Issues) |
| 09 | [Принципы Go-стиля](09-go-skills-applied.md) | Инженерные принципы Go-кода и их выражение в репозитории |

## TL;DR — что это за сервис

Один из domain-сервисов Kachō (владелец Account/Project — `kacho-iam`).
Owns два слоя:

- **VPC ресурсы**: Network, Subnet, Address (v4/v6),
  `NetworkInterface` (first-class NIC), RouteTable, SecurityGroup,
  Gateway. Public API на gRPC `:9090`, через api-gateway → REST
  `/vpc/v1/...`. Project-scoped (ссылка на kacho-iam.Project; DB-колонка
  `project_id` — legacy-имя = id владельца-проекта). Admin-операции
  (default-SG setter, IPAM) — через `Internal*` на `:9091`.
- **IPAM (kacho-only, admin)**: AddressPool + network_default binding.
  Internal-only API на gRPC `:9091`. Глобальные
  ресурсы — не привязаны к org/cloud/project. Управляются админом через web-UI /
  curl-REST на api-gateway internal mux. (Region/Zone — домен `kacho-geo`.)

Cascade IP-allocate работает inline в worker'е `AddressService.doCreate`.

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
       │    │        │ ProjectClient      │
       │    │        └──→ kacho-iam        │
       │    │             (gRPC)           │
       │    │             ProjectService.Get
       │    │             project_id → account_id
       │    │                              │
       │    ▼                              │
       │  ┌──────────────────┐            │
       │  │  pg-vpc (own DB) │            │
       │  └──────────────────┘            │
       └──────────────────────────────────┘
```

Внешние зависимости:
- `kacho-iam.ProjectService.Get` — для existence check владельца-проекта
  (`project_id` — legacy-имя колонки = id владельца-проекта) и для resolve
  `project_id → account_id` в IPAM cascade.
- `kacho-corelib` — `ids`, `operations`, `db`, `grpcsrv`, `outbox`, etc.
- `kacho-proto` — все .proto, generated stubs.

VPC **не знает** про:
- api-gateway (просто слушает 9090/9091).
- UI/TUI/CLI (это REST/gRPC потребители).
- compute/loadbalancer (общение только по API).

См. [`02-data-flows.md`](02-data-flows.md#cross-service-project-cloud-id-lookup).
