# 01 — Resources

Детально по каждому ресурсу. Поля, инварианты, связи, спецчастности.

## Иерархия и связи

```mermaid
erDiagram
  NETWORK ||--o{ SUBNET : has
  NETWORK ||--o{ ROUTE_TABLE : has
  NETWORK ||--o{ SECURITY_GROUP : has
  NETWORK ||--o| SECURITY_GROUP : "default_security_group_id"
  SUBNET ||--o{ ADDRESS : "internal IP"
  SUBNET }o--|| ZONE : "zone_id"
  ZONE }o--|| REGION : "region_id"
  ADDRESS_POOL }o--o| ZONE : "zone_id (nullable=global)"
  ADDRESS }o--o| ADDRESS_POOL : "external_ipv4.address_pool_id"
  ADDRESS_POOL_NETWORK_DEFAULT }o--|| NETWORK : "PK"
  ADDRESS_POOL_NETWORK_DEFAULT }o--|| ADDRESS_POOL : "FK"
  ADDRESS_POOL_ADDRESS_OVERRIDE }o--|| ADDRESS : "PK"
  ADDRESS_POOL_ADDRESS_OVERRIDE }o--|| ADDRESS_POOL : "FK"
  CLOUD_POOL_SELECTOR }o--|| ADDRESS_POOL : "matches via selector"
```

## Public ресурсы (verbatim YC, folder-scoped)

### Network

Контейнер для Subnet/RouteTable/SG. Базовая VPC-сеть.

| Поле | Тип | Замечания |
|---|---|---|
| `id` | text PK, prefix `enp` | |
| `folder_id` | text NOT NULL | `networks_folder_id_name_key` UNIQUE(folder_id, name) |
| `name` | text | NameVPC permissive |
| `description` | text | ≤256 |
| `labels` | jsonb | ≤64 пар |
| `default_security_group_id` | text NULL FK→`security_groups` | устанавливается inline в `doCreate` при `KACHO_VPC_DEFAULT_SG_INLINE=true` (default). ON DELETE SET NULL |
| `created_at` | tstz | в proto-ответе truncate до секунд |

**Инварианты**:
- При Create (`KACHO_VPC_DEFAULT_SG_INLINE=true`, default) — атомарно создаётся
  Network + Default SG + биндинг `default_security_group_id` в одной TX worker'а.
  Раньше это был отдельный reconciler в `kacho-vpc-controllers` — упразднён в Phase 2.
  При `=false` Network создаётся без SG (для load-тестов / внешнего reconciler'а).
- `Move` (между folder'ами) — отдельный RPC.
- Hard-delete; FK от Subnet/RT/SG = RESTRICT.

### Subnet

Подсеть в Network, привязана к Zone.

| Поле | Тип | Замечания |
|---|---|---|
| `id` | text PK, prefix `e9b` | |
| `folder_id`, `network_id`, `zone_id` | text NOT NULL | immutable после Create; `subnets_folder_id_name_key` UNIQUE(folder_id, name) WHERE name<>'' (миграция 0002) |
| `name`, `description`, `labels` | | |
| `v4_cidr_blocks` | text[] | array, главный — `[0]` |
| `v6_cidr_blocks` | text[] | dual-stack |
| `v4_cidr_primary` | text computed | для EXCLUDE constraint (см. ниже) |
| `route_table_id` | text NULL FK→`route_tables` | optional |
| `dhcp_options` | jsonb | domain_name (RFC 1123), dns/ntp servers |

**Инварианты**:
- CIDR overlap **запрещён** в пределах Network — DB-level через
  `EXCLUDE USING gist` (миграция 0007). Маппится в `FailedPrecondition
  "Subnet CIDRs can not overlap"` (verbatim YC).
- `Relocate` отвергается, если у Subnet есть Address-ресурсы (verbatim YC
  `FailedPrecondition "Invalid subnet state"`).
- `AddCidrBlocks` второй+ CIDR не покрывается DB EXCLUDE (constraint
  смотрит только на `v4_cidr_primary`). Защищено сервис-level через
  `networkRepo.List` cross-check.

### Address

External (folder-scoped public IP) или internal (IP в Subnet).

| Поле | Тип | Замечания |
|---|---|---|
| `id` | text PK, prefix `e9b` | |
| `folder_id` | text NOT NULL | |
| `addr_type` | smallint | 0=unspec, 1=external, 2=internal |
| `ip_version` | smallint | |
| `external_ipv4` | jsonb | `{address, zone_id, address_pool_id, requirements}` |
| `internal_ipv4` | jsonb | `{address, subnet_id}` |
| `internal_subnet_id` | text computed | для UNIQUE per subnet |
| `reserved`, `used` | bool | computed на сервис-стороне |
| `deletion_protection` | bool | sync-check перед Delete |

**UNIQUE constraints**:
- `addresses_folder_id_name_key` PARTIAL UNIQUE на `(folder_id, name)`
  WHERE name `<>` `''` (миграция `0002`) — дубль непустого `name` в folder → `ALREADY_EXISTS`.
- `addresses_external_ip_uniq` PARTIAL UNIQUE на
  `external_ipv4 ->> 'address'` WHERE address `<>` `''` — запрещает
  дубль external IP глобально (не считая пустых allocate-pending).
- `addresses_external_pool_ip_uniq` PARTIAL UNIQUE на
  `(address_pool_id, address)` — запрещает повторный pick того же IP
  в том же pool.
- `addresses_internal_subnet_ip_uniq` PARTIAL UNIQUE на
  `(internal_subnet_id, address)` — запрещает дубль internal IP в Subnet.

**Allocate flow** см. [`02-data-flows.md`](02-data-flows.md#address-allocate-cascade).

### RouteTable

Static routes для Network. Один RT может быть привязан к нескольким
Subnet'ам.

| Поле | Замечания |
|---|---|
| `id` (prefix `enp`), `folder_id`, `network_id` immutable | UNIQUE(folder_id, name) WHERE name<>'' (миграция 0002) |
| `static_routes` jsonb array | full-replace на Update |
| `name`, `description`, `labels` | |

### SecurityGroup

Firewall rules, привязан к Network. Один SG может быть `default_for_network`.

| Поле | Замечания |
|---|---|
| `id` (prefix `enp`), `folder_id`, `network_id` immutable | UNIQUE(folder_id, name) WHERE name<>'' (миграция 0002) |
| `status` | text |
| `default_for_network` | bool — `true` у inline-создаваемой default SG (если `KACHO_VPC_DEFAULT_SG_INLINE=true`) |
| `rules` | jsonb array (см. SgRulesEditor в UI / proto SecurityGroupRule) |

**RPC специфика**:
- `UpdateRules` — полный replace массива.
- `UpdateRule` — патч одного правила по `rule_id`.
- Optimistic concurrency через `xmin::text` (zero-overhead, без отдельной
  колонки).

### Gateway

Shared egress (NAT-style), не привязан к Network.

| Поле | Замечания |
|---|---|
| `id` (prefix `enp`), `folder_id` | UNIQUE(folder_id, name) WHERE name<>'' (миграция 0002) |
| `shared_egress_gateway` | nested message |

### PrivateEndpoint

Privatelink connection: Network + Subnet → external service.

| Поле | Замечания |
|---|---|
| `id` (prefix `enp`), `folder_id`, `network_id` | UNIQUE(folder_id, name) WHERE name<>'' (миграция 0002) |
| `subnet_id` / `address_id` / `ip_address` | через `address_spec` oneof (`internal_ipv4_address_spec.subnet_id` или `address_id`); опциональны |
| `service_type` | сейчас только `object_storage` |

## Internal/admin ресурсы (kacho-only, глобальные)

### Region

Глобальный admin-only географический ресурс.

| Поле | Тип | Замечания |
|---|---|---|
| `id` | text PK | строка `ru-central1` |
| `name` | text | human-readable |
| `created_at` | tstz | |

- Seed: миграция 0019 (`ru-central1`).
- FK: `zones.region_id` (RESTRICT).
- API: `InternalRegionService.{Create,Get,List,Update,Delete}`.

### Zone

Зона в регионе.

| Поле | Тип | Замечания |
|---|---|---|
| `id` | text PK | строка `ru-central1-a` |
| `region_id` | text NOT NULL FK→regions | RESTRICT |
| `name` | text | UNIQUE(region_id, name) WHERE name<>'' (миграция 0022) |
| `created_at` | tstz | |

- Seed: миграция 0019 (`ru-central1-{a,b,d}`).
- FK: `address_pools.zone_id` (RESTRICT).
- При Create — sync NotFound check, что `region_id` существует.

### AddressPool

Глобальный admin-only пул external IP.

| Поле | Тип | Замечания |
|---|---|---|
| `id` | text PK, prefix `apl` | |
| `name`, `description`, `labels` | | |
| `cidr_blocks` | text[] | IPv4 CIDR-блоки |
| `kind` | smallint | 1=EXTERNAL_PUBLIC, 2=EXTERNAL_TEST, 100=RESERVED_INTERNAL |
| `zone_id` | text NULL FK→zones | NULL = глобальный fallback |
| `is_default` | bool | partial UNIQUE на (COALESCE(zone_id,''), kind) WHERE is_default |
| `selector_labels` | jsonb | для cascade Step 3 |
| `selector_priority` | int | tie-break при equal-specificity |

- API: `InternalAddressPoolService` (CRUD + bindings + diagnostics +
  observability — см. [04-api-surface.md](04-api-surface.md)).
- ID prefix `apl` (3 символа — обязательный формат `corelib/ids`).
- НЕТ `folder_id` (миграция 0021 убрала — pool глобальный).

### CloudPoolSelector

Admin-controlled labels на Cloud для IPAM cascade Step 3.

| Поле | Тип | Замечания |
|---|---|---|
| `cloud_id` | text PK | external ID — указывает на rm.clouds.id, кросс-DB FK нет |
| `selector` | jsonb | GIN индекс для @>-запросов |
| `set_at`, `set_by` | tstz, text | audit |

- API: `InternalCloudService.{Set,Unset,Get}PoolSelector`.
- Раньше был `network_pool_selector` — выпилили миграцией 0022, потому что
  external Address не имеет network_id и cascade не срабатывал. Вместо него
  cloud-уровень: `folder.cloud_id → cloud_pool_selector`.

### Bindings (служебные таблицы)

`address_pool_network_default(network_id PK, pool_id)`:
- Для cascade Step 2 (internal IP path).
- API: `BindAsNetworkDefault / UnbindNetworkDefault`.

`address_pool_address_override(address_id PK, pool_id)`:
- Для cascade Step 1 (per-address override).
- Применим только если у Address ещё нет allocated IP.
- API: `BindAsAddressOverride / UnbindAddressOverride`.

## Что не VPC-ресурс, но рядом живёт

- `vpc_outbox` — таблица событий (resource_type/resource_id/op/payload).
  Триггер `pg_notify('vpc_outbox', sequence_no)` для подписчиков.
  Подписчик — `InternalWatchService.Watch`, дёргается серверными
  компонентами (UI пока не использует — оно по polling).

- `operations` — синхронизирована из `kacho-corelib/migrations/common`
  через `make sync-migrations`. Не редактировать локально.

См. полную схему БД и список миграций → [05-database.md](05-database.md).
