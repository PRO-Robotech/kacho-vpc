# Within-service refs audit — DB-уровневое покрытие constraints (KAC-84)

> **Контекст**
>
> Этот документ — полный аудит ссылочных полей и инвариантов всех таблиц схемы
> `kacho_vpc` против правила workspace `CLAUDE.md` § «Within-service refs — DB-уровень
> обязателен» (запрет #10): любая ссылочная зависимость **внутри одной БД сервиса**
> и любой инвариант должны быть зафиксированы на уровне Postgres-constraint
> (FK / partial UNIQUE / EXCLUDE / CHECK / atomic conditional UPDATE c CAS /
> `FOR UPDATE SKIP LOCKED`). Software-side `Get → check → Update` запрещён —
> это TOCTOU-prone (см. инцидент 2026-05-14, KAC-52: два конкурентных
> `Compute.Instance.Create` с одним `existing_network_interface_id` оба прошли
> software-guard, second writer wins → два pod-а на одной NIC).
>
> Источник истины:
> - Миграции `internal/migrations/0001_initial.sql` (squashed baseline) +
>   `0002..0022_*.sql` (delta).
> - Service-слой `internal/service/*.go` (software-prechecks как UX-layer).
> - Repo-слой `internal/repo/*.go` (DDL-маппинг ошибок в sentinel-errors).
>
> **Cross-service ссылки** (`folder_id`, `zone_id`, `cloud_id`, `subnet.zone_id` → kacho-compute,
> `nic.instance_id` через `used_by_id` → kacho-compute) — **out of scope**: для них
> DB-уровневые FK невозможны (`database-per-service` запрет #8), валидация делается
> в worker'е через peer-API + грациозный dangling-ref. Audit касается **только**
> рёбер графа в пределах одной БД `kacho_vpc`.

---

## Summary

- **Проверено**: 14 ресурсных/служебных таблиц, **42 ссылочных поля / инварианта**.
- **Покрыто DB-уровнем** (FK / partial UNIQUE / EXCLUDE / CHECK / CAS / SKIP LOCKED): **36**.
- **Gap'ы выявлены**: **6**.
- **Рекомендуемых миграций**: **5** (один gap безопасен по дизайну, документируется; см. G6).

Все gap'ы — это **software-side TOCTOU / unenforced uniqueness / unenforced existence**.
Ни один из них не вызвал production-инцидент на момент аудита (2026-05-15), но
архитектурно соответствуют тому же классу, что инцидент NIC-attach race KAC-52.

| #  | Gap | Severity | Тип нарушения |
|----|-----|----------|----------------|
| G1 | `address_references.SetReference` upserts через `ON CONFLICT DO UPDATE` без CAS — молча перетирает чужого referrer'а | **High** (parity c KAC-52) | TOCTOU + missing CAS |
| G2 | `private_endpoints.network_id / subnet_id / address_id` — нет FK | **High** | Missing within-service FK |
| G3 | `networks.default_security_group_id` — нет FK на `security_groups(id)` | Medium | Missing within-service FK |
| G4 | `security_groups.default_for_network` — нет partial UNIQUE `(network_id) WHERE default_for_network` | Medium | Missing UNIQUE constraint |
| G5 | enum-like колонки (`addresses.addr_type/ip_version`, `gateways.gateway_type`, `private_endpoints.service_type/status`, `network_interfaces.status`, `address_pools.kind`) — нет CHECK | Low | Missing CHECK constraint |
| G6 | `vpn_id_free` pop через `DELETE … WHERE id = (SELECT … LIMIT 1)` без `FOR UPDATE SKIP LOCKED` — корректно, но потенциально pessimal под нагрузкой | Info (by-design) | Pessimal under load, корректный fallback |

Полные таблицы coverage и детальные рекомендации миграций — ниже.

---

## 1. Полная таблица coverage

Колонки таблицы:
- **Resource.field / invariant** — что проверяем.
- **Что гарантируется** — продуктовый инвариант.
- **DB constraint** — Postgres-механизм (✅ есть / ❌ отсутствует / N/A — cross-service).
- **Software check** — есть ли дублирующий software-precheck (для UX).
- **Решение** — OK / G<n> (отсылка к gap-секции) / N/A.

### 1.1 `networks`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `networks_pkey` ✅ | n/a | OK |
| `folder_id` | существует в `kacho-resource-manager` | N/A (cross-service) | `FolderClient.Exists` в worker'е | OK (cross-service) |
| `(folder_id, name)` | уникальный non-empty name на folder | `networks_folder_id_name_key` UNIQUE non-partial ✅ | redundant List+name check (для UX) | OK |
| `vpn_id` | уникальный 24-bit (1..16777215) | `networks_vpn_id_key` UNIQUE + `MAXVALUE 16777215` на sequence ✅ | n/a (генерится в repo) | OK |
| `vpn_id` allocation race | atomic pop из freelist или nextval | `WITH popped AS (DELETE … LIMIT 1)` + `COALESCE(popped, nextval('vpn_id_seq'))` — atomic в одном statement ✅ | n/a | OK (см. G6 — pessimal под concurrency, но корректно) |
| `default_security_group_id` | если не пустой — указывает на существующий SG в этой же сети | ❌ нет FK, нет CHECK на принадлежность сети | inline в `network.go::doCreate` создаёт SG и UPDATE'ит поле; явная установка через Update — без валидации | **G3** |

### 1.2 `subnets`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `subnets_pkey` ✅ | n/a | OK |
| `folder_id` | существует в RM | N/A (cross-service) | `FolderClient.Exists` | OK (cross-service) |
| `(folder_id, name)` | уникальный non-empty name | `subnets_folder_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ (0002) | n/a | OK |
| `network_id` | существует, не nullable | `subnets_network_id_fkey` FK (no explicit ON DELETE — default NO ACTION, эквивалент RESTRICT) ✅ + `NOT NULL` ✅ | redundant `networkRepo.Get` в `doCreate` | OK |
| `zone_id` | существует в compute | N/A (cross-service после KAC-15) | `compute.ZoneService.Get` через `GeographyRegistry` | OK (cross-service) |
| `route_table_id` | если задан — указывает на существующую RT (можно `NULL`) | `subnets_route_table_id_fkey` FK ON DELETE SET NULL ✅ (0019) | trigger `subnet_auto_pick_rt_trg` BEFORE INSERT подставляет автоматически | OK |
| `v4_cidr_blocks[1]` | непересечение с другими subnets той же сети по v4 | `subnets_no_overlap_v4` EXCLUDE USING gist `(network_id WITH =, v4_cidr_primary inet_ops WITH &&)` ✅ | sync `checkCIDRDisjoint` в `subnet.go` | OK |
| `v6_cidr_blocks[1]` | аналогично, по v6 | `subnets_no_overlap_v6` EXCLUDE ✅ | sync check | OK |
| `v4_cidr_blocks[2..n]` (multi-CIDR Subnet через AddCidrBlocks) | непересечение | ⚠️ EXCLUDE check'ит только primary (array[1]) | `networkRepo.List` ручная проверка в `subnet.go:382-388` | OK (документировано как known limitation в `docs/architecture/05-database.md`; addCidr — admin-path, не tenant) |
| `route_table_id` auto-association | при INSERT RT — UPDATE всех subnets без RT, эмит `Subnet.UPDATED` | `rt_auto_assoc_subnets_trg` AFTER INSERT ✅ (0019) + `subnets_outbox_emit_route_table_change_trg` AFTER UPDATE OF ✅ | n/a (DB-driven) | OK |

### 1.3 `addresses`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `addresses_pkey` ✅ | n/a | OK |
| `folder_id` | существует в RM | N/A (cross-service) | `FolderClient.Exists` | OK (cross-service) |
| `(folder_id, name)` | уникальный non-empty | `addresses_folder_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ (0002) | n/a | OK |
| `internal_subnet_id` (generated col) | если internal v4/v6 задан — subnet существует, RESTRICT удаления | `addresses_internal_subnet_fkey` FK ON DELETE RESTRICT ✅ (generated col: 0001 v4, расширено в 0013 на v6) + `addresses_internal_subnet_idx` | sync `AddressesBySubnet` precheck в `SubnetService.Delete` | OK |
| `external_ipv4 ->> 'address'` | глобально уникальный (один IP — один Address-row) | `addresses_external_ip_uniq` partial UNIQUE ✅ | retry на 23505 в allocator | OK |
| `(external_ipv4 ->> 'address_pool_id', external_ipv4 ->> 'address')` | один IP внутри pool — один Address | `addresses_external_pool_ip_uniq` partial UNIQUE ✅ | retry on 23505 в `address_repo.go:643` `FOR UPDATE SKIP LOCKED` allocator | OK |
| `(internal_ipv4 ->> 'subnet_id', internal_ipv4 ->> 'address')` | один internal v4 на subnet | `addresses_internal_subnet_ip_uniq` partial UNIQUE ✅ | retry on 23505 в random-pick | OK |
| `(internal_ipv6 ->> 'subnet_id', internal_ipv6 ->> 'address')` | один internal v6 на subnet | `addresses_internal_subnet_ipv6_uniq` partial UNIQUE ✅ (0009) | retry on 23505 | OK |
| `(external_ipv6 ->> 'address_pool_id', external_ipv6 ->> 'address')` | один external v6 IP внутри pool — один Address | `addresses_external_v6_pool_ip_uniq` partial UNIQUE ✅ (0021) | retry on 23505 | OK |
| `external_ipv4 IS NOT NULL OR internal_ipv4 IS NOT NULL OR internal_ipv6 IS NOT NULL` | как минимум один spec заполнен | ❌ нет CHECK — формально пустой row возможен | sync proto oneof validation в `address.go` | acceptable (oneof enforced на API-уровне; raw INSERT бы создал «пустой» Address, но это не угрожает безопасности) |
| `addr_type` (smallint) | значение из enum (EXTERNAL/INTERNAL) | ❌ нет CHECK | sync маппинг enum в service | **G5** (minor) |
| `ip_version` (smallint) | значение из enum (IPV4/IPV6) | ❌ нет CHECK | sync маппинг | **G5** (minor) |

### 1.4 `address_references`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `address_id` PK | один referrer на адрес | `PRIMARY KEY (address_id)` ✅ | n/a | OK |
| `address_id` → `addresses(id)` | существует, CASCADE при удалении Address | FK ON DELETE CASCADE ✅ (0003) | n/a | OK |
| `addresses.used` ↔ existence of referrer row | синхронность в одной tx | в одной tx (`SetReference` / `ClearReference` обновляют оба поля под одним BEGIN/COMMIT) ✅ | n/a | OK (по структуре tx) |
| **attach race** — упрощённое присваивание referrer'a без CAS — silent overwrite | один Address одновременно использует один NIC; конкурирующий attach должен fail | ❌ `INSERT … ON CONFLICT (address_id) DO UPDATE` молча перетирает existing referrer; нет CAS-условия `WHERE referrer_id = '' OR = $new`; нет partial UNIQUE для backstop | sync `validateAddressRef` делает `Get → if a.Used → fail`. TOCTOU между check и SetReference: writer A читает `used=false`, writer B читает `used=false`, оба идут в SetReference → B's `ON CONFLICT DO UPDATE` молча перетирает A's referrer | **G1** (high severity — точная parity c KAC-52 для NIC) |

### 1.5 `network_interfaces`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `network_interfaces_pkey` (inline в 0006) ✅ | n/a | OK |
| `folder_id` | существует в RM | N/A (cross-service) | `FolderClient.Exists` | OK (cross-service) |
| `(folder_id, name)` | уникальный non-empty | `network_interfaces_folder_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ (0006) | n/a | OK |
| `subnet_id` | существует, RESTRICT удаления Subnet | FK ON DELETE RESTRICT ✅ (0006 → 0011 CASCADE → 0012 RESTRICT откатил) | sync `NICsBySubnet` precheck в `SubnetService.Delete` | OK |
| `mac_address` | cloud-wide UNIQUE, NOT NULL | `network_interfaces_mac_address_key` UNIQUE ✅ + `NOT NULL` ✅ (0014) | retry on 23505 collision (`ErrMacCollision`) в `service.doCreate` | OK |
| `jsonb_array_length(v4_address_ids) ≤ 1` | максимум 1 v4 на NIC | CHECK `network_interfaces_v4_addr_max1` ✅ (0018) | sync `validateNICAddressCardinality` | OK |
| `jsonb_array_length(v6_address_ids) ≤ 1` | максимум 1 v6 на NIC | CHECK `network_interfaces_v6_addr_max1` ✅ (0018) | sync check | OK |
| `v4_address_ids[*]` references addresses(id) | каждый id существует, RESTRICT/CASCADE? | ❌ нет FK (jsonb-массив, нельзя поставить FK напрямую) | semantic guard: address `used=true` + referrer-row; `AddressService.Delete` блокирует delete пока address in use NIC'ом (`KAC-31`). Каскад "удалить NIC → addresses.used=false" — best-effort в `NetworkInterfaceService.doDelete` через `detachAddresses` | acceptable (jsonb-массив не поддерживает FK; backstop через `addresses.used` + `address_references` partial-FK; см. G1 для attach race) |
| `v6_address_ids[*]` references | то же | то же | то же | то же |
| `used_by_id` attach race | атомарный set-if-free-or-same | CAS atomic UPDATE: `UPDATE network_interfaces SET used_by_*=... WHERE id=$1 AND (used_by_id='' OR used_by_id=$new) RETURNING ...` ✅ (`network_interface_repo.go:354-357`); 0 rows из RETURNING → `ErrFailedPrecondition` | n/a (DB CAS — единственная защита, software guard убран после KAC-52) | OK (race-proof per workspace CLAUDE.md шаблон CAS) |
| `used_by_type/id/name` co-clearing на detach | атомарно очищаются вместе | single-statement UPDATE с обновлением всех 3-х колонок в одном выражении ✅ | n/a | OK |
| `status` (TEXT enum: PROVISIONING/ACTIVE/AVAILABLE/FAILED/DELETING) | значение из enum | ❌ нет CHECK | sync mapping в `niStatusName` | **G5** (minor) |

### 1.6 `route_tables`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `route_tables_pkey` ✅ | n/a | OK |
| `folder_id` | существует в RM | N/A (cross-service) | `FolderClient.Exists` | OK (cross-service) |
| `(folder_id, name)` | уникальный non-empty | `route_tables_folder_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ (0002) | n/a | OK |
| `network_id` | существует, RESTRICT удаления | `route_tables_network_id_fkey` FK ✅ (default NO ACTION = RESTRICT) | sync `checkNetworkEmpty` в `Network.Delete` | OK |
| auto-assoc subnets при Insert | новая RT применяется к subnets с `route_table_id IS NULL` той же сети | `rt_auto_assoc_subnets_trg` AFTER INSERT ✅ (0019) | n/a | OK |
| `static_routes` JSONB items | валидные CIDR / IP, нет dup destination | ❌ нет CHECK | sync `validateStaticRoutes` | acceptable (валидация в request-path; raw INSERT мусора не реалистичен — нет admin-API писать static_routes напрямую) |

### 1.7 `security_groups`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `security_groups_pkey` ✅ | n/a | OK |
| `folder_id` | существует в RM | N/A (cross-service) | `FolderClient.Exists` | OK (cross-service) |
| `(folder_id, name)` | уникальный non-empty | `security_groups_folder_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ (0002) | n/a | OK |
| `network_id` | если задан — существует, RESTRICT удаления | `security_groups_network_id_fkey` FK ON DELETE RESTRICT ✅; nullable после 0010 (unbound SG) | sync через `Get(networkID)` в `doCreate` | OK |
| `(network_id) WHERE default_for_network = true` | один default SG на сеть | ❌ нет partial UNIQUE | inline в `network.go::doCreate` создаёт один default SG; UPDATE `default_for_network` через сервис — без uniqueness-guard | **G4** |
| `rules` JSONB OCC | concurrent UpdateRules без lost update | conditional UPDATE с `WHERE xmin::text = $expected` + `RETURNING` ✅ (`security_group_repo.go:282/359`); 0 rows → `ErrFailedPrecondition` | n/a (xmin OCC pattern) | OK |
| individual rule.id within rules array | уникальный rule.id | ❌ jsonb-массив, нельзя UNIQUE per-element | sync check duplicate id в `service/security_group.go` | acceptable (denorm rules-в-JSONB by design; per-element UNIQUE требует нормализации в отдельную таблицу — отдельный feature, не gap relative to current schema) |
| `status` (TEXT enum: ACTIVE) | значение из enum | ❌ нет CHECK | sync setting | **G5** (minor) |

### 1.8 `gateways`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `gateways_pkey` ✅ | n/a | OK |
| `folder_id` | существует в RM | N/A (cross-service) | `FolderClient.Exists` | OK (cross-service) |
| `(folder_id, name)` | уникальный non-empty | `gateways_folder_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ (0002) | n/a | OK |
| `gateway_type` (TEXT default 'shared_egress') | значение из enum | ❌ нет CHECK | sync (default-only сейчас) | **G5** (minor) |

### 1.9 `private_endpoints`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `private_endpoints_pkey` ✅ | n/a | OK |
| `folder_id` | существует в RM | N/A (cross-service) | `FolderClient.Exists` | OK (cross-service) |
| `(folder_id, name)` | уникальный non-empty | `private_endpoints_folder_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ (0002) | n/a | OK |
| `network_id` | если задан — существует Network в этой же БД | ❌ **нет FK** (nullable TEXT) | sync `networkRepo.Get` в `doCreate` | **G2** |
| `subnet_id` | если задан — существует Subnet | ❌ **нет FK** | sync `subnetRepo.Get` если непустой | **G2** |
| `address_id` | если задан — существует Address | ❌ **нет FK** | sync (зависит от service-path; PE.Create не валидирует address_id явно) | **G2** |
| `service_type` (TEXT default 'object_storage') | значение из enum | ❌ нет CHECK | sync default-fill | **G5** (minor) |
| `status` (TEXT default 'PENDING') | значение из enum (PENDING/ACTIVE/FAILED) | ❌ нет CHECK | sync | **G5** (minor) |

### 1.10 `address_pools`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `address_pools_pkey` ✅ | n/a | OK |
| `zone_id` (TEXT, nullable) | если задан — существует zone в compute | N/A (cross-service после KAC-15) | `compute.ZoneService.Get` | OK (cross-service) |
| `(COALESCE(zone_id,''), kind) WHERE is_default = true` | один default pool на (zone, kind) | `address_pools_zone_kind_default_uniq` partial UNIQUE ✅ | n/a | OK |
| `kind` (smallint) | значение из enum (EXTERNAL_PUBLIC / EXTERNAL_TEST / RESERVED_INTERNAL) | ❌ нет CHECK | sync mapping | **G5** (minor) |
| `cardinality(v4_cidr_blocks) + cardinality(v6_cidr_blocks) > 0` (после KAC-71) | pool не пустой | ❌ нет CHECK (защита только в request-path, через REQ-IPL-CR/UPD) + defensive guard в 0022 миграции на старую `cidr_blocks` колонку | sync validate в Create/Update | acceptable (sync validation покрывает API-path; raw INSERT — admin-only, edge case) |

### 1.11 `address_pool_address_override`

| Resource.field / invariant | Что гарантируется | DB constraint | Решение |
|---|---|---|---|
| `address_id` PK | один override на address | `PRIMARY KEY (address_id)` ✅ | OK |
| `address_id` → addresses | CASCADE при delete address | FK ON DELETE CASCADE ✅ | OK |
| `pool_id` → address_pools | RESTRICT при delete pool | FK ON DELETE RESTRICT ✅ | OK |

### 1.12 `address_pool_network_default`

| Resource.field / invariant | Что гарантируется | DB constraint | Решение |
|---|---|---|---|
| `network_id` PK | один default pool на network | `PRIMARY KEY (network_id)` ✅ | OK |
| `network_id` → networks | CASCADE при delete network | FK ON DELETE CASCADE ✅ | OK |
| `pool_id` → address_pools | RESTRICT при delete pool | FK ON DELETE RESTRICT ✅ | OK |

### 1.13 `cloud_pool_selector`

| Resource.field / invariant | Что гарантируется | DB constraint | Решение |
|---|---|---|---|
| `cloud_id` PK | один selector на cloud | `PRIMARY KEY (cloud_id)` ✅ | OK |
| `cloud_id` → kacho-resource-manager.Cloud | N/A (cross-service) | OK (cross-service) |

### 1.14 `address_pool_free_ips`

| Resource.field / invariant | Что гарантируется | DB constraint | Решение |
|---|---|---|---|
| `(pool_id, ip)` PK | один IP не может быть в freelist дважды | PK ✅ | OK |
| `pool_id` → address_pools | CASCADE при удалении pool | FK ON DELETE CASCADE ✅ | OK |
| concurrent allocate без contention | atomic pop ровно одной row | `WITH picked AS (SELECT ip … LIMIT 1 FOR UPDATE SKIP LOCKED) DELETE … USING picked RETURNING ip` ✅ | OK |

### 1.15 `ipv6_pool_cursors`, `ipv6_allocated_ips`, `ipv6_released_offsets`

| Resource.field / invariant | Что гарантируется | DB constraint | Решение |
|---|---|---|---|
| `ipv6_pool_cursors.pool_id` PK + FK | один cursor на pool, CASCADE | PK + FK CASCADE ✅ | OK |
| `ipv6_allocated_ips (pool_id, ip)` PK | один IP — один Address внутри pool | PK ✅ | OK |
| `ipv6_allocated_ips (pool_id, offset)` UNIQUE | один offset — один IP (interference-free allocate) | UNIQUE ✅ | OK |
| `ipv6_released_offsets (pool_id, offset)` PK | offset переиспользуется не более раза | PK ✅ + FK CASCADE | OK |
| pop released offset под concurrency | atomic | `FOR UPDATE SKIP LOCKED` в `address_repo_ipv6.go:77` ✅ | OK |

### 1.16 `vpc_outbox`, `vpc_watch_cursors`, `operations`, `vpn_id_free`

| Table.field / invariant | Что гарантируется | DB constraint | Решение |
|---|---|---|---|
| `vpc_outbox.sequence_no` PK + sequence | строго возрастающий, уникальный | `PRIMARY KEY` + `nextval('vpc_outbox_sequence_no_seq')` default ✅ | OK |
| `vpc_outbox_notify_trg` AFTER INSERT | каждый INSERT → `pg_notify('vpc_outbox', sequence_no)` | trigger ✅ | OK |
| outbox row atomicity с ресурс-row | в одной tx | все `emitVPC` вызовы — в той же tx, что INSERT/UPDATE ресурса (см. `outbox.go`); review-rule ✅ | OK |
| `vpc_watch_cursors.subscriber_id` PK | один cursor на subscriber | PK ✅ | OK |
| `operations.id` PK | уникальный | PK ✅ | OK |
| `vpn_id_free.id` PK | освобождённые vpn_id переиспользуются ровно раз | PK ✅ | OK |
| `vpn_id_free` pop без contention | один winner за allocate | atomic `WITH popped AS (DELETE … WHERE id = (SELECT id … LIMIT 1) RETURNING id) INSERT … COALESCE(popped, nextval())` ✅ — корректно, но **без `FOR UPDATE SKIP LOCKED`** | OK (см. **G6** — info-level, не gap) |

---

## 2. Детализация gap'ов

### G1 — `address_references.SetReference` upserts через `ON CONFLICT DO UPDATE` без CAS (silent steal)

**Severity**: High — точный parity-case c инцидентом KAC-52 (NIC-attach race 2026-05-14).

**Контекст.** Address-ресурс может быть «использован» (`addresses.used = true` + row в `address_references`). Кто его использует — записано в `address_references.referrer_{type,id,name}`. Используется `NetworkInterfaceService.validateAndAttachAddresses` для привязки address к NIC.

**Текущая реализация** (`internal/repo/address_repo.go:400-435` + service `internal/service/network_interface.go:296-317`):

```go
// 1) service-слой: software-guard (TOCTOU window!)
func (s *NetworkInterfaceService) validateAddressRef(ctx context.Context, id, ...) error {
    a, err := s.addressRepo.Get(ctx, id)        // (a) SELECT
    if err != nil { ... }
    if a.Used {                                  // (b) check
        return status.Errorf(codes.FailedPrecondition, "address %s is already in use", id)
    }
    return nil
}

// 2) repo: unconditional upsert
func (r *AddressRepo) SetReference(ctx, ref) (*domain.AddressReference, error) {
    // UPDATE addresses SET used = true WHERE id = $1                  ← unconditional
    // INSERT INTO address_references (...) VALUES (...)
    //   ON CONFLICT (address_id) DO UPDATE                            ← silent overwrite
    //     SET referrer_type = EXCLUDED.referrer_type,
    //         referrer_id   = EXCLUDED.referrer_id, ...
```

**Race-сценарий**:
1. Address `addr-X` свободен (`used=false`, нет referrer-row).
2. Worker `T_A` (для NIC `nic-A`): `Get(addr-X)` → `used=false` → проходит guard.
3. Worker `T_B` (для NIC `nic-B`): `Get(addr-X)` → `used=false` → проходит guard.
4. `T_A` `SetReference(addr-X, ref=nic-A)`: вставляет row → `address_references.referrer_id = nic-A`, `used = true`. Commit.
5. `T_B` `SetReference(addr-X, ref=nic-B)`: `INSERT ... ON CONFLICT DO UPDATE` → **молча перетирает** `referrer_id` на `nic-B`. Commit.

**Результат**: addr-X в JSONB-массиве `nic-A.v4_address_ids` И `nic-B.v4_address_ids`, но `address_references` указывает на `nic-B`. NIC `nic-A` теперь имеет dangling address-ref → data-plane получает противоречивое состояние.

**Точный аналог KAC-52** для NIC-attach race на адресе.

**Предлагаемая миграция** (`0023_address_references_cas.sql`):

```sql
-- +goose Up
-- KAC-84: address_references.SetReference race fix (parity c KAC-52).
-- INSERT … ON CONFLICT (address_id) DO UPDATE — молча перетирал чужого
-- referrer'a. Workspace CLAUDE.md §«Within-service refs» §«Атомарный CAS»:
-- "either free or already same owner".
--
-- Schema-level invariant: enforce "максимум один referrer на address" через
-- existing PK (= уже есть). Race-proof attach делаем в repo через
-- conditional INSERT … ON CONFLICT … WHERE referrer_id = EXCLUDED.referrer_id
-- (CAS pattern для re-attach к same referrer). Никаких новых constraint'ов
-- не требуется — fix чисто на repo-уровне.

-- Этот файл — no-op (только документация решения). Реальный код:
--   repo.SetReference (address_repo.go:400+) переписать на CAS:
--     INSERT INTO address_references (address_id, referrer_type, referrer_id, referrer_name, attached_at)
--     VALUES ($1, $2, $3, $4, now())
--     ON CONFLICT (address_id) DO UPDATE
--       SET attached_at = now()
--     WHERE address_references.referrer_id = EXCLUDED.referrer_id     -- ← CAS!
--     RETURNING ...;
--   0 rows из RETURNING + проверка `cur_referrer_id` через предварительный
--   SELECT FOR UPDATE → service.ErrFailedPrecondition.
--   Или альтернатива — UPDATE addresses SET used = true WHERE id = $1
--                                          AND (used = false OR EXISTS (
--                                            SELECT 1 FROM address_references
--                                             WHERE address_id = $1 AND referrer_id = $new))
--   (атомарный single-statement CAS на addresses.used).
SELECT 1;

-- +goose Down
SELECT 1;
```

> **Note**: G1 решается в первую очередь **кодом** (repo-CAS), а не миграцией.
> Миграция может быть «нулевая» (как 0016 — placeholder для goose) ИЛИ можно
> добавить **partial UNIQUE на `(addresses.id) WHERE used=true`** как backstop —
> но семантически это тот же дизайн-pitfall, что 0016/0017 для NIC, поэтому
> предпочтительнее CAS-only.
>
> **Integration test** обязателен (см. CLAUDE.md запрет #11): два goroutine
> параллельно SetReference на один address — ровно один winner, второй —
> `FailedPrecondition`. Зеркалит `network_interface_attach_race_integration_test.go`.

---

### G2 — `private_endpoints` lacks within-service FK на `network_id`, `subnet_id`, `address_id`

**Severity**: High — нарушение запрета #10 для трёх ссылочных полей.

**Контекст**: `private_endpoints` хранит ссылки на Network / Subnet / Address (см. `0001_initial.sql:156-170`). Все три — within-service ссылки (живут в той же БД), но FK отсутствуют. Существование валидируется software в `PrivateEndpointService.doCreate` (`network_repo.Get` / `subnet_repo.Get`).

**Race-сценарий**:
1. `PE.Create(network=N1, subnet=S1)`: worker делает `networkRepo.Get(N1)` → OK.
2. Параллельно: оператор удаляет N1 (через прямой DB-доступ ИЛИ через Network.Delete если у N1 нет других children — представимый сценарий).
3. Worker PE: `repo.Insert(pe)` — INSERT проходит, потому что нет FK → PE с dangling `network_id`.
4. Worker далее: `subnet_repo.Get(S1)` → если S1 ещё не CASCADE-удалена (например, был unbinding) — OK; если уже удалена → ошибка, PE с битой ссылкой остаётся.

Симметрично для `subnet_id` и `address_id`.

**Дополнительный риск**: при удалении Network/Subnet/Address не происходит каскадного действия на PE — никаких FK ON DELETE RESTRICT/SET NULL не сработает, PE-ы остаются как orphan'ы.

**Предлагаемая миграция** (`0023_private_endpoint_fks.sql`):

```sql
-- +goose Up
-- KAC-84: enforce within-service FK constraints on private_endpoints —
-- network_id, subnet_id, address_id были чисто software-validated в
-- PrivateEndpointService.doCreate (TOCTOU + orphan'ы при удалении ресурса).
-- Workspace CLAUDE.md §«Within-service refs — DB-уровень обязателен».
--
-- ON DELETE:
--   network_id  — RESTRICT (PE жёстко удерживает Network; consistent с verbatim
--                 YC, где PE — child of Network: «network is not empty»).
--   subnet_id   — RESTRICT (если задан; nullable — PE без subnet допустим).
--   address_id  — RESTRICT (PE удерживает Address — нельзя удалить занятый;
--                 alternatively, можно SET NULL, если PE-проект разрешает
--                 «PE без выделенного address»).

-- Backfill dangling refs → NULL (если есть; PE-ресурс новый, dangling маловероятен,
-- но миграция должна быть data-safe).
UPDATE private_endpoints SET network_id = NULL
 WHERE network_id IS NOT NULL AND network_id <> ''
   AND NOT EXISTS (SELECT 1 FROM networks WHERE id = private_endpoints.network_id);
UPDATE private_endpoints SET network_id = NULL WHERE network_id = '';

UPDATE private_endpoints SET subnet_id = NULL
 WHERE subnet_id IS NOT NULL AND subnet_id <> ''
   AND NOT EXISTS (SELECT 1 FROM subnets WHERE id = private_endpoints.subnet_id);
UPDATE private_endpoints SET subnet_id = NULL WHERE subnet_id = '';

UPDATE private_endpoints SET address_id = NULL
 WHERE address_id IS NOT NULL AND address_id <> ''
   AND NOT EXISTS (SELECT 1 FROM addresses WHERE id = private_endpoints.address_id);
UPDATE private_endpoints SET address_id = NULL WHERE address_id = '';

-- NOT VALID + VALIDATE — два прохода для безопасного ADD CONSTRAINT под нагрузкой.
ALTER TABLE private_endpoints
  ADD CONSTRAINT private_endpoints_network_id_fkey
    FOREIGN KEY (network_id) REFERENCES networks(id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE private_endpoints VALIDATE CONSTRAINT private_endpoints_network_id_fkey;

ALTER TABLE private_endpoints
  ADD CONSTRAINT private_endpoints_subnet_id_fkey
    FOREIGN KEY (subnet_id) REFERENCES subnets(id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE private_endpoints VALIDATE CONSTRAINT private_endpoints_subnet_id_fkey;

ALTER TABLE private_endpoints
  ADD CONSTRAINT private_endpoints_address_id_fkey
    FOREIGN KEY (address_id) REFERENCES addresses(id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE private_endpoints VALIDATE CONSTRAINT private_endpoints_address_id_fkey;

-- +goose Down
ALTER TABLE private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_address_id_fkey;
ALTER TABLE private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_subnet_id_fkey;
ALTER TABLE private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_network_id_fkey;
```

> **Note**: после добавления FK нужно адаптировать service-слой:
> `Network.checkNetworkEmpty` должен включать `private_endpoints` (текущая
> реализация проверяет subnets / route_tables / non-default SGs); FK RESTRICT
> станет async backstop. Аналогично для Subnet.Delete и Address.Delete.
> **Integration tests**: delete network with PE → FailedPrecondition; delete
> subnet with PE → FailedPrecondition.

---

### G3 — `networks.default_security_group_id` lacks FK на `security_groups(id)`

**Severity**: Medium — invariant «default SG существует и принадлежит этой Network» только software-enforced.

**Контекст**: `networks.default_security_group_id TEXT NOT NULL DEFAULT ''` (см. `0001_initial.sql:125`). Поле заполняется inline в `NetworkService.doCreate` (`UPDATE networks SET default_security_group_id = sg.id`). Read-modify-write через service-слой; FK на DB-уровне нет.

**Risk-сценарии**:
1. **Race на delete default-SG**: Network.Delete worker сначала пытается удалить default SG, потом Network. Между этими двумя UPDATE'ами кто-то может прочитать `networks.default_security_group_id` и попытаться использовать SG, который уже удалён → dangling-ref.
2. **Inconsistent state**: ничего не мешает прямым UPDATE через psql установить `default_security_group_id = 'enpXX-random'` (несуществующий или принадлежащий другой сети SG). Repo-код это примет без ошибки.
3. **Network → default SG → Network — circular dependency**: default SG имеет FK `network_id REFERENCES networks ON DELETE RESTRICT` (миграция 0001 + relaxed nullable 0010). Network.Delete worker делает `s.sgService.repo.Delete(default_sg_id)` FIRST, потом сама Network.Delete. Если этот порядок нарушен (или default-SG-id указывает на SG другой сети) — FK RESTRICT не позволит удалить Network. С FK ON DELETE SET NULL это решится автоматически.

**Предлагаемая миграция** (`0024_networks_default_sg_fk.sql`):

```sql
-- +goose Up
-- KAC-84: default_security_group_id — within-service ref без FK. Workspace
-- CLAUDE.md §«Within-service refs». ON DELETE SET NULL: при удалении SG из
-- этой колонки она становится пустой, не блокируя ничего; default-SG-creation
-- в NetworkService.doCreate проставит новый id при необходимости (или Network
-- останется без default SG — допустимый state при KACHO_VPC_DEFAULT_SG_INLINE=false).
--
-- ВНИМАНИЕ: поле NOT NULL DEFAULT '' — пустая строка не валидна как FK-ссылка.
-- Конвертируем '' → NULL backfill'ом, потом ALTER COLUMN DROP NOT NULL.

ALTER TABLE networks ALTER COLUMN default_security_group_id DROP NOT NULL;
UPDATE networks SET default_security_group_id = NULL
 WHERE default_security_group_id = ''
    OR (default_security_group_id IS NOT NULL
        AND NOT EXISTS (SELECT 1 FROM security_groups WHERE id = networks.default_security_group_id));

ALTER TABLE networks
  ADD CONSTRAINT networks_default_security_group_id_fkey
    FOREIGN KEY (default_security_group_id)
    REFERENCES security_groups(id)
    ON DELETE SET NULL
    NOT VALID;
ALTER TABLE networks VALIDATE CONSTRAINT networks_default_security_group_id_fkey;

-- +goose Down
ALTER TABLE networks DROP CONSTRAINT IF EXISTS networks_default_security_group_id_fkey;
-- restoring NOT NULL невозможно — потребовало бы fill всех NULL значений.
-- Best-effort: '' default остаётся, новых NULL не появляется.
UPDATE networks SET default_security_group_id = '' WHERE default_security_group_id IS NULL;
ALTER TABLE networks ALTER COLUMN default_security_group_id SET NOT NULL;
```

> **Note**: миграция меняет nullability колонки → требует адаптации Go-кода
> (`NetworkRepo` / `Network` domain-struct работают с `string`; nullable
> вернётся как пустая строка — semantically совместимо). Code-change тривиальный.

---

### G4 — `security_groups.default_for_network` lacks partial UNIQUE on `(network_id) WHERE default_for_network = true`

**Severity**: Medium — invariant «один default SG на сеть» не enforced.

**Контекст**: `security_groups.default_for_network BOOLEAN NOT NULL DEFAULT false` (см. `0001_initial.sql:213`). Inline в `NetworkService.doCreate` создаётся ровно один default-SG с этим флагом. Но ничего не мешает:
1. Прямому UPDATE другой SG в той же сети `SET default_for_network = true`.
2. Concurrent двум `Network.Create` для разных параметров создать два «default»-SG (теоретически невозможно для одной Network, но при concurrent UPDATE поля — да).

**Предлагаемая миграция** (`0025_sg_default_for_network_uniq.sql`):

```sql
-- +goose Up
-- KAC-84: один default SG на network — invariant, который должен быть на
-- DB-уровне. Workspace CLAUDE.md §«Within-service refs» partial UNIQUE.
CREATE UNIQUE INDEX security_groups_default_for_network_uniq
  ON security_groups (network_id)
  WHERE default_for_network = true AND network_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS security_groups_default_for_network_uniq;
```

> **Note**: одновременно нужен **CHECK**, что `default_for_network = true`
> возможен только при `network_id IS NOT NULL` (unbound SG не может быть
> default-for-network ничего):
>
> ```sql
> ALTER TABLE security_groups
>   ADD CONSTRAINT security_groups_default_requires_network
>     CHECK (default_for_network = false OR network_id IS NOT NULL);
> ```
>
> Pre-flight: проверить отсутствие row с `default_for_network=true AND network_id IS NULL`.

---

### G5 — Enum-like columns без CHECK constraints

**Severity**: Low — surface area для прямых INSERT-ов «мусора»; в текущем service-flow не достижимо.

**Затрагивает поля**:
- `addresses.addr_type smallint`, `addresses.ip_version smallint`
- `network_interfaces.status TEXT`
- `gateways.gateway_type TEXT`
- `private_endpoints.service_type TEXT`, `private_endpoints.status TEXT`
- `address_pools.kind smallint`
- `security_groups.status TEXT`

Все эти поля имеют **известный конечный набор valid values** (enum из proto / domain). Service-слой всегда пишет валидное значение. Но прямой `psql` UPDATE / SQL-инъекция через ORM (которого нет, но гипотетически) / migration с typo / dump-restore не из текущей версии — могут вставить мусор.

**Предлагаемая миграция** (`0026_enum_checks.sql`):

```sql
-- +goose Up
-- KAC-84: добавить CHECK constraints на enum-like колонки. Workspace
-- CLAUDE.md §«Within-service refs» — инвариант «значение из enum» на DB-уровне.

ALTER TABLE addresses
  ADD CONSTRAINT addresses_addr_type_check CHECK (addr_type IN (0, 1, 2)),     -- UNSPECIFIED/INTERNAL/EXTERNAL
  ADD CONSTRAINT addresses_ip_version_check CHECK (ip_version IN (0, 1, 2));   -- UNSPECIFIED/IPV4/IPV6

ALTER TABLE network_interfaces
  ADD CONSTRAINT network_interfaces_status_check
    CHECK (status IN ('PROVISIONING','ACTIVE','AVAILABLE','FAILED','DELETING'));

ALTER TABLE gateways
  ADD CONSTRAINT gateways_gateway_type_check
    CHECK (gateway_type IN ('shared_egress'));   -- расширить enum при добавлении новых типов

ALTER TABLE private_endpoints
  ADD CONSTRAINT private_endpoints_service_type_check
    CHECK (service_type IN ('object_storage', 'container_registry'))     -- TBD
  ADD CONSTRAINT private_endpoints_status_check
    CHECK (status IN ('PENDING','ACTIVE','FAILED'));

ALTER TABLE address_pools
  ADD CONSTRAINT address_pools_kind_check CHECK (kind IN (0, 1, 2, 3));   -- UNSPECIFIED/EXTERNAL_PUBLIC/EXTERNAL_TEST/RESERVED_INTERNAL

ALTER TABLE security_groups
  ADD CONSTRAINT security_groups_status_check CHECK (status IN ('ACTIVE'));   -- расширить при добавлении

-- +goose Down
ALTER TABLE security_groups DROP CONSTRAINT IF EXISTS security_groups_status_check;
ALTER TABLE address_pools DROP CONSTRAINT IF EXISTS address_pools_kind_check;
ALTER TABLE private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_status_check;
ALTER TABLE private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_service_type_check;
ALTER TABLE gateways DROP CONSTRAINT IF EXISTS gateways_gateway_type_check;
ALTER TABLE network_interfaces DROP CONSTRAINT IF EXISTS network_interfaces_status_check;
ALTER TABLE addresses DROP CONSTRAINT IF EXISTS addresses_ip_version_check;
ALTER TABLE addresses DROP CONSTRAINT IF EXISTS addresses_addr_type_check;
```

> **Pre-flight**: каждый CHECK сначала проверяется через `SELECT … WHERE NOT (…)`
> на live стенде; при существовании невалидных row'ов миграция нагнётся, и нужно
> fill'ить значения вручную до повторного apply.
> **Maintenance**: при добавлении нового enum value в proto — нужно расширить CHECK
> миграцией; есть risk забыть. Альтернатива: использовать Postgres `CREATE TYPE … AS ENUM`
> (но запрет #5 — не редактировать применённые миграции — усложнит расширение типа).
> Текущий выбор `TEXT CHECK (… IN (…))` — самый maintainable для эволюционирующего API.

---

### G6 — `vpn_id_free` pop без `FOR UPDATE SKIP LOCKED`

**Severity**: Info (по дизайну корректно, под нагрузкой pessimal).

**Контекст** (`internal/repo/network_repo.go:131-137`):

```sql
WITH popped AS (
  DELETE FROM vpn_id_free
   WHERE id = (SELECT id FROM vpn_id_free ORDER BY id LIMIT 1)
   RETURNING id
)
INSERT INTO networks (..., vpn_id)
VALUES (..., COALESCE((SELECT id FROM popped), nextval('vpn_id_seq')::int));
```

Под concurrency:
1. Tx_A и Tx_B оба SELECT'ят `id = (SELECT id FROM vpn_id_free LIMIT 1)` → видят `id = X`.
2. Tx_A DELETE'ит `WHERE id = X` — берёт row-lock.
3. Tx_B пытается DELETE'нуть `WHERE id = X` — блокируется на row-lock, ждёт commit'a Tx_A.
4. Tx_A commits → row X удалён.
5. Tx_B видит uniformly «no row matched» → CTE `popped` пустой → COALESCE → `nextval()`.

**Это корректно** (нет double-assignment vpn_id). Но Tx_B всегда **ждёт** Tx_A
вместо «попробовать другую row сразу». Под high-concurrency Network.Create
serialized на этой row-lock'е. Альтернатива:

```sql
WITH picked AS (
  SELECT id FROM vpn_id_free ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED
),
popped AS (
  DELETE FROM vpn_id_free WHERE id IN (SELECT id FROM picked) RETURNING id
)
INSERT INTO networks (..., vpn_id)
VALUES (..., COALESCE((SELECT id FROM popped), nextval('vpn_id_seq')::int));
```

**Решение**: не блокер; оставить как есть до load-test'а Network.Create
(>500 RPS). Документировать в `docs/architecture/05-database.md` как known
trade-off. **No migration needed** для этого audit'а — фикс попадёт в отдельный
performance-ticket если/когда возникнет.

---

## 3. Сводная таблица «исправлено / open»

| Категория | Field/invariant | Status |
|---|---|---|
| **Closed (DB-уровневое покрытие — OK)** | | |
| Все PK уникальности | 14 таблиц | ✅ |
| `(folder_id, name)` UNIQUE по 7 ресурсам | networks/subnets/route_tables/security_groups/gateways/private_endpoints/addresses/network_interfaces | ✅ |
| `subnets.network_id` FK ON DELETE RESTRICT | | ✅ |
| `subnets.route_table_id` FK ON DELETE SET NULL + auto-assoc triggers | | ✅ (KAC-56) |
| Subnet CIDR overlap | EXCLUDE USING gist | ✅ |
| `addresses.internal_subnet_id` (generated, v4∪v6) FK RESTRICT | | ✅ (KAC-34) |
| `route_tables.network_id` FK | | ✅ |
| `security_groups.network_id` FK ON DELETE RESTRICT (nullable) | | ✅ |
| `network_interfaces.subnet_id` FK ON DELETE RESTRICT | | ✅ (KAC-33) |
| NIC v4/v6 address cardinality ≤ 1 | CHECK | ✅ (KAC-55) |
| NIC `used_by_id` attach race | atomic CAS | ✅ (KAC-52) |
| NIC `mac_address` cloud-wide UNIQUE | | ✅ (KAC-48) |
| Address external IP global UNIQUE (v4, v6) | partial UNIQUE | ✅ |
| Address internal IP per-subnet UNIQUE (v4, v6) | partial UNIQUE | ✅ |
| Address pool freelist atomic pop | `FOR UPDATE SKIP LOCKED` | ✅ |
| Address pool default per (zone, kind) | partial UNIQUE | ✅ |
| Address_pool_address_override / network_default FKs | CASCADE / RESTRICT | ✅ |
| IPv6 cursor / allocated / released — atomic pop | `FOR UPDATE SKIP LOCKED` + UNIQUE | ✅ |
| SecurityGroup rules OCC | `xmin::text` CAS | ✅ |
| Outbox sequence + emit-в-той-же-tx | trigger + repo convention | ✅ |
| vpn_id allocation atomic | CTE + COALESCE(popped, nextval) | ✅ (G6: pessimal under load) |
| **Open (gap'ы)** | | |
| `address_references` attach race | TOCTOU + silent steal | **G1** (High) |
| `private_endpoints.network_id/subnet_id/address_id` no FK | within-service ref software-only | **G2** (High) |
| `networks.default_security_group_id` no FK | within-service ref software-only | **G3** (Medium) |
| `(security_groups.network_id) WHERE default_for_network` not UNIQUE | invariant software-only | **G4** (Medium) |
| Enum-like columns no CHECK | 7 колонок (status / type / kind) | **G5** (Low) |
| `vpn_id_free` pop без SKIP LOCKED | pessimal under load (корректно) | **G6** (Info) |

---

## 4. Рекомендация по follow-up

- Создать **KAC-87 epic** «within-service refs DB-coverage closure» с subtask'ами:
  - **KAC-87.1** — G1 fix (address_references CAS): repo-rewrite + integration-test mirror'ит `network_interface_attach_race_integration_test.go`.
  - **KAC-87.2** — G2 fix (private_endpoints FKs): migration `0023_private_endpoint_fks.sql` + расширить `Network.checkNetworkEmpty` / `Subnet.Delete` / `Address.Delete` software-precheck'и на PE.
  - **KAC-87.3** — G3 fix (networks.default_security_group_id FK): migration `0024_networks_default_sg_fk.sql` + adapt repo на nullable column.
  - **KAC-87.4** — G4 fix (default_for_network partial UNIQUE): migration `0025_sg_default_for_network_uniq.sql` + CHECK `default_for_network ⇒ network_id IS NOT NULL`.
  - **KAC-87.5** — G5 fix (enum CHECK constraints): migration `0026_enum_checks.sql` (pre-flight scan, fill bad rows, enable). Maintenance plan: расширять при добавлении новых enum values в proto.
  - **KAC-87.6** (defer) — G6: load-test driven — fix вместе с другими hot-path optimizations.

- Каждый subtask: **integration-test обязателен** (запрет #11). Шаблон — `network_interface_attach_race_integration_test.go` (concurrent goroutines на спорный путь, ровно один winner).

- Перед merge каждой миграции — pre-flight на dev-стенде: `SELECT … WHERE …` запросом проверить, что existing rows не нарушают новый constraint; при найденных нарушениях — backfill в той же миграции (как сделано в `0019_vpc_auto_associations.sql` для dangling refs).

---

## 5. Ссылки

- Workspace CLAUDE.md §«Within-service refs — DB-уровень обязателен» / запрет #10
- KAC-52 — NIC-attach race, инцидент 2026-05-14 (источник pattern'а для G1)
- KAC-55, KAC-56, KAC-33, KAC-34, KAC-48 — предыдущие DB-coverage эпики (см. `01-resources.md`, `05-database.md`)
- `internal/migrations/0001_initial.sql..0022_addresspool_split_cidr_family.sql`
- `internal/repo/network_interface_repo.go:332` — эталон atomic CAS pattern (`SetUsedBy`)
- `internal/repo/address_repo.go:643` — эталон `FOR UPDATE SKIP LOCKED` pattern (external IPv4 alloc)
- `internal/repo/security_group_repo.go:282/359` — эталон `xmin` OCC pattern
- `internal/repo/network_interface_attach_race_integration_test.go` — эталонный race-integration-test
