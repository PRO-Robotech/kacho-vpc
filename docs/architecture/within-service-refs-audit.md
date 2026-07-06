# Within-service refs audit — DB-уровневое покрытие constraints

> **Контекст**
>
> Этот документ — аудит ссылочных полей и инвариантов всех таблиц схемы `kacho_vpc`
> против правила «within-service инварианты — только на DB-уровне»: любая ссылочная
> зависимость **внутри одной БД сервиса** и любой инвариант должны быть зафиксированы на
> уровне Postgres-constraint (FK / partial UNIQUE / EXCLUDE / CHECK / atomic conditional
> UPDATE c CAS / `FOR UPDATE SKIP LOCKED`). Software-side `Get → check → Update` запрещен —
> это TOCTOU-prone.
>
> Источник истины:
> - Миграции `internal/migrations/0001_initial.sql` (базовая схема) + `0002..0009_*.sql` (delta).
> - Service-слой `internal/apps/kacho/api/<resource>/*.go` (software-prechecks как UX-layer).
> - Repo-слой `internal/repo/kacho/pg/*.go` (DDL-маппинг ошибок в sentinel-errors).
>
> **Cross-service ссылки** (`project_id` → kacho-iam, `zone_id` → kacho-geo,
> `nic.used_by_id` → kacho-compute) — **out of scope**: для них DB-уровневые FK невозможны
> (database-per-service), валидация делается на request-path через peer-API + грациозный
> dangling-ref. Audit касается **только** ребер графа в пределах одной БД `kacho_vpc`.

---

## Summary

- **Проверено**: ресурсные/служебные таблицы схемы `kacho_vpc`, все ссылочные поля и инварианты.
- **Покрыто DB-уровнем** (FK / partial UNIQUE / EXCLUDE / CHECK / CAS / SKIP LOCKED): все
  существенные within-service инварианты.
- **Остаточные пункты** — описаны в разделе 2: enum-like колонки без CHECK (G5, Low) и
  `network_interfaces.security_group_ids` — within-service ref без FK/software-check
  (G6, Medium; известный gap [#27](https://github.com/PRO-Robotech/kacho-vpc/issues/27),
  фикс — join-table отдельным behavioral-PR).

Ключевые within-service инварианты закрыты на DB-уровне:

| Инвариант | Механизм |
|---|---|
| `(project_id, name)` уникальность по 7 ресурсам | UNIQUE / partial UNIQUE |
| Subnet CIDR overlap | EXCLUDE USING gist (v4 + v6) |
| Address internal-subnet RESTRICT | generated col `internal_subnet_id` + FK |
| NIC v4/v6 address cardinality ≤ 1 | CHECK |
| NIC `used_by_id` attach race | atomic CAS |
| NIC `mac_address` cloud-wide UNIQUE | UNIQUE + CHECK regex |
| Address external/internal IP уникальность | partial UNIQUE (v4 + v6) |
| Address pool freelist atomic pop | `FOR UPDATE SKIP LOCKED` |
| Address pool CIDR overlap per kind | EXCLUDE USING gist (`address_pool_cidrs`, мигр. 0004) |
| Address pool default per (zone, kind) | partial UNIQUE |
| `networks.default_security_group_id` существование | FK ON DELETE SET NULL (мигр. 0005) |
| Один default-SG на сеть | partial UNIQUE (мигр. 0005) |
| SecurityGroup rules OCC | `xmin::text` CAS |

---

## 1. Полная таблица coverage

Колонки таблицы:
- **Resource.field / invariant** — что проверяем.
- **Что гарантируется** — продуктовый инвариант.
- **DB constraint** — Postgres-механизм (✅ есть / ❌ отсутствует / N/A — cross-service).
- **Software check** — есть ли дублирующий software-precheck (для UX).
- **Решение** — OK / G<n> (отсылка к разделу 2) / N/A.

### 1.1 `networks`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `networks_pkey` ✅ | n/a | OK |
| `project_id` | существует в `kacho-iam` | N/A (cross-service) | `ProjectClient.Exists` | OK (cross-service) |
| `(project_id, name)` | уникальный name на project | `networks_project_id_name_key` UNIQUE ✅ | redundant List+name check (для UX) | OK |
| `default_security_group_id` | если не пустой — указывает на существующий SG | `networks_default_security_group_fk` FK ON DELETE SET NULL ✅ (0005) | inline в `network.go::doCreate` | OK |
| `vrf_id` | уникальный per-network | `networks_vrf_id_key` UNIQUE ✅ (0007) + sequence-backed | n/a (DB-allocated) | OK |

### 1.2 `subnets`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `subnets_pkey` ✅ | n/a | OK |
| `project_id` | существует в `kacho-iam` | N/A (cross-service) | `ProjectClient.Exists` | OK (cross-service) |
| `(project_id, name)` | уникальный non-empty name | `subnets_project_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ | n/a | OK |
| `network_id` | существует, не nullable | `subnets_network_id_fkey` FK (NO ACTION = RESTRICT) ✅ + `NOT NULL` ✅ | redundant `networkRepo.Get` в `doCreate` | OK |
| `zone_id` | существует в kacho-geo | N/A (cross-service) | `geo.ZoneService.Get` через `ZoneRegistry` | OK (cross-service) |
| `route_table_id` | если задан — указывает на существующую RT (можно `NULL`) | `subnets_route_table_id_fkey` FK ON DELETE SET NULL ✅ | trigger `subnet_auto_pick_rt_trg` BEFORE INSERT | OK |
| `v4_cidr_blocks[1]` | непересечение с другими subnets той же сети по v4 | `subnets_no_overlap_v4` EXCLUDE USING gist ✅ | sync `checkCIDRDisjoint` в `subnet.go` | OK |
| `v6_cidr_blocks[1]` | аналогично, по v6 | `subnets_no_overlap_v6` EXCLUDE ✅ | sync check | OK |
| `v4_cidr_blocks[2..n]` (multi-CIDR через AddCidrBlocks) | непересечение | ⚠️ EXCLUDE check'ит только primary (array[1]) | `networkRepo.List` ручная проверка в `subnet.go` | OK (документировано как known limitation; addCidr — admin-path) |
| `route_table_id` auto-association | при INSERT RT — UPDATE всех subnets без RT, эмит `Subnet.UPDATED` | `rt_auto_assoc_subnets_trg` AFTER INSERT ✅ + `subnets_outbox_emit_route_table_change_trg` AFTER UPDATE OF ✅ | n/a (DB-driven) | OK |

### 1.3 `addresses`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `addresses_pkey` ✅ | n/a | OK |
| `project_id` | существует в `kacho-iam` | N/A (cross-service) | `ProjectClient.Exists` | OK (cross-service) |
| `(project_id, name)` | уникальный non-empty | `addresses_project_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ | n/a | OK |
| `internal_subnet_id` (generated col) | если internal v4/v6 задан — subnet существует, RESTRICT удаления | `addresses_internal_subnet_fkey` FK ON DELETE RESTRICT ✅ (generated col покрывает v4+v6) | sync `AddressesBySubnet` precheck в `SubnetService.Delete` | OK |
| `external_ipv4 ->> 'address'` | глобально уникальный | `addresses_external_ip_uniq` partial UNIQUE ✅ | retry на 23505 в allocator | OK |
| `(external_ipv4 ->> 'address_pool_id', address)` | один IP внутри pool | `addresses_external_pool_ip_uniq` partial UNIQUE ✅ | retry on 23505 в `FOR UPDATE SKIP LOCKED` allocator | OK |
| `(internal_ipv4 ->> 'subnet_id', address)` | один internal v4 на subnet | `addresses_internal_subnet_ip_uniq` partial UNIQUE ✅ | retry on 23505 | OK |
| `(internal_ipv6 ->> 'subnet_id', address)` | один internal v6 на subnet | `addresses_internal_subnet_ipv6_uniq` partial UNIQUE ✅ | retry on 23505 | OK |
| `(external_ipv6 ->> 'address_pool_id', address)` | один external v6 IP внутри pool | `addresses_external_v6_pool_ip_uniq` partial UNIQUE ✅ | retry on 23505 | OK |
| at-least-one-spec | как минимум один spec заполнен | ❌ нет CHECK | sync proto oneof validation в `address.go` | acceptable (oneof enforced на API-уровне) |
| `addr_type` (smallint) | значение из enum | ❌ нет CHECK | sync маппинг enum в service | **G5** (minor) |
| `ip_version` (smallint) | значение из enum | ❌ нет CHECK | sync маппинг | **G5** (minor) |

### 1.4 `address_references`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `address_id` PK | один referrer на адрес | `PRIMARY KEY (address_id)` ✅ | n/a | OK |
| `address_id` → `addresses(id)` | существует, CASCADE при удалении Address | FK ON DELETE CASCADE ✅ | n/a | OK |
| `addresses.used` ↔ existence of referrer row | синхронность в одной tx | `SetReference` / `ClearReference` обновляют оба поля под одним BEGIN/COMMIT ✅ | n/a | OK |
| attach race — один Address ≤ одному referrer | конкурирующий attach должен fail | CAS-условие в `SetReference` (`WHERE referrer_id = '' OR = $new`) + PK как backstop ✅ | sync `validateAddressRef` (Get → if `Used` → fail) как UX-layer | OK |

### 1.5 `network_interfaces`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `network_interfaces_pkey` ✅ | n/a | OK |
| `project_id` | существует в `kacho-iam` | N/A (cross-service) | `ProjectClient.Exists` | OK (cross-service) |
| `(project_id, name)` | уникальный non-empty | `network_interfaces_project_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ | n/a | OK |
| `subnet_id` | существует, RESTRICT удаления Subnet | FK ON DELETE RESTRICT ✅ | sync `NICsBySubnet` precheck в `SubnetService.Delete` | OK |
| `mac_address` | cloud-wide UNIQUE, NOT NULL, формат | `network_interfaces_mac_address_key` UNIQUE ✅ + `NOT NULL` ✅ + CHECK regex ✅ | retry on 23505 collision (`ErrMacCollision`) | OK |
| `jsonb_array_length(v4_address_ids) ≤ 1` | максимум 1 v4 на NIC | CHECK `network_interfaces_v4_addr_max1` ✅ | sync `validateNICAddressCardinality` | OK |
| `jsonb_array_length(v6_address_ids) ≤ 1` | максимум 1 v6 на NIC | CHECK `network_interfaces_v6_addr_max1` ✅ | sync check | OK |
| `v4_address_ids[*]` / `v6_address_ids[*]` references | каждый id существует | ❌ нет FK (jsonb-массив) | semantic guard: address `used=true` + referrer-row; `AddressService.Delete` блокирует пока address in use | acceptable (jsonb-массив не поддерживает FK; backstop через `addresses.used` + `address_references`) |
| `security_group_ids[*]` references | каждый SG существует | ❌ нет FK/join-table (jsonb-массив) | ❌ software-check тоже отсутствует (NIC use-case не имеет SecurityGroups-порта); `SG.Delete` гардит только `DefaultForNetwork`, `repo.Delete` безусловен | **G6** — within-service refcheck ОТСУТСТВУЕТ (в отличие от v4/v6 address_ids здесь нет backstop). Dangling ref после `SG.Delete`; известный gap [#27](https://github.com/PRO-Robotech/kacho-vpc/issues/27), red-тест `SG-DEL-NEG-NIC-ATTACHED` |
| `used_by_id` attach race | атомарный set-if-free-or-same | CAS atomic UPDATE: `UPDATE … WHERE id=$1 AND (used_by_id='' OR used_by_id=$new) RETURNING …` ✅; 0 rows → `ErrFailedPrecondition` | n/a (DB CAS — единственная защита) | OK |
| `used_by_type/id/name` co-clearing на detach | атомарно очищаются вместе | single-statement UPDATE всех 3-х колонок ✅ | n/a | OK |
| `status` (TEXT enum) | значение из enum | CHECK `network_interfaces_status_check` ✅ | sync mapping в `niStatusName` | OK |

### 1.6 `route_tables`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `route_tables_pkey` ✅ | n/a | OK |
| `project_id` | существует в `kacho-iam` | N/A (cross-service) | `ProjectClient.Exists` | OK (cross-service) |
| `(project_id, name)` | уникальный non-empty | `route_tables_project_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ | n/a | OK |
| `network_id` | существует, RESTRICT удаления | `route_tables_network_id_fkey` FK ✅ (NO ACTION = RESTRICT) | sync `checkNetworkEmpty` в `Network.Delete` | OK |
| auto-assoc subnets при Insert | новая RT применяется к subnets с `route_table_id IS NULL` той же сети | `rt_auto_assoc_subnets_trg` AFTER INSERT ✅ | n/a | OK |
| `static_routes` JSONB items | валидные CIDR / IP, нет dup destination | ❌ нет CHECK | sync `validateStaticRoutes` | acceptable (валидация в request-path; нет admin-API писать static_routes напрямую) |

### 1.7 `security_groups`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `security_groups_pkey` ✅ | n/a | OK |
| `project_id` | существует в `kacho-iam` | N/A (cross-service) | `ProjectClient.Exists` | OK (cross-service) |
| `(project_id, name)` | уникальный non-empty | `security_groups_project_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ | n/a | OK |
| `network_id` | если задан — существует, RESTRICT удаления | `security_groups_network_id_fkey` FK ON DELETE RESTRICT ✅; nullable (unbound SG) | sync через `Get(networkID)` в `doCreate` | OK |
| `(network_id) WHERE default_for_network = true` | один default SG на сеть | `security_groups_one_default_per_network` partial UNIQUE ✅ (0005) | inline в `network.go::doCreate` | OK |
| `rules` JSONB OCC | concurrent UpdateRules без lost update | conditional UPDATE с `WHERE xmin::text = $expected` + `RETURNING` ✅; 0 rows → `ErrFailedPrecondition` | n/a (xmin OCC pattern) | OK |
| individual rule.id within rules array | уникальный rule.id | ❌ jsonb-массив, нельзя UNIQUE per-element | sync check duplicate id в `security_group.go` | acceptable (denorm rules-в-JSONB by design) |

### 1.8 `gateways`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `gateways_pkey` ✅ | n/a | OK |
| `project_id` | существует в `kacho-iam` | N/A (cross-service) | `ProjectClient.Exists` | OK (cross-service) |
| `(project_id, name)` | уникальный non-empty | `gateways_project_id_name_key` partial UNIQUE WHERE `name <> ''` ✅ | n/a | OK |
| `gateway_type` (TEXT default 'shared_egress') | значение из enum | ❌ нет CHECK | sync (default-only сейчас) | **G5** (minor) |

### 1.9 `address_pools`

| Resource.field / invariant | Что гарантируется | DB constraint | Software check | Решение |
|---|---|---|---|---|
| `id` PK | уникальный | `address_pools_pkey` ✅ | n/a | OK |
| `zone_id` (TEXT, nullable) | если задан — существует zone в kacho-geo | N/A (cross-service) | `geo.ZoneService.Get` | OK (cross-service) |
| CIDR пулов непересечение per kind | глобально непересекающиеся CIDR одного kind | `address_pool_cidrs` EXCLUDE USING gist ✅ (0004) | sync validate в Create/Update | OK |
| `(COALESCE(zone_id,''), kind) WHERE is_default = true` | один default pool на (zone, kind) | `address_pools_zone_kind_default_uniq` partial UNIQUE ✅ | n/a | OK |
| `kind` (smallint) | значение из enum | ❌ нет CHECK | sync mapping | **G5** (minor) |
| pool не пустой | хотя бы один CIDR | ❌ нет CHECK | sync validate в Create/Update | acceptable (sync validation покрывает API-path; raw INSERT — admin-only) |

### 1.10 `address_pool_network_default`

| Resource.field / invariant | Что гарантируется | DB constraint | Решение |
|---|---|---|---|
| `network_id` PK | один default pool на network | `PRIMARY KEY (network_id)` ✅ | OK |
| `network_id` → networks | CASCADE при delete network | FK ON DELETE CASCADE ✅ | OK |
| `pool_id` → address_pools | RESTRICT при delete pool | FK ON DELETE RESTRICT ✅ | OK |

### 1.11 `address_pool_cidrs` (мигр. 0004)

| Resource.field / invariant | Что гарантируется | DB constraint | Решение |
|---|---|---|---|
| `(pool_id, block)` | CIDR не дублируется | составной ключ + EXCLUDE ✅ | OK |
| `pool_id` → address_pools | CASCADE при delete pool | FK ON DELETE CASCADE ✅ | OK |
| CIDR непересечение per kind | declarative, race-free | `EXCLUDE USING gist (kind WITH =, block inet_ops WITH &&)` ✅ | OK |

### 1.12 `address_pool_free_ips`

| Resource.field / invariant | Что гарантируется | DB constraint | Решение |
|---|---|---|---|
| `(pool_id, ip)` PK | один IP не может быть в freelist дважды | PK ✅ | OK |
| `pool_id` → address_pools | CASCADE при удалении pool | FK ON DELETE CASCADE ✅ | OK |
| concurrent allocate без contention | atomic pop ровно одной row | `WITH picked AS (SELECT ip … LIMIT 1 FOR UPDATE SKIP LOCKED) DELETE … USING picked RETURNING ip` ✅ | OK |

### 1.13 `ipv6_pool_cursors`, `ipv6_allocated_ips`, `ipv6_released_offsets`

| Resource.field / invariant | Что гарантируется | DB constraint | Решение |
|---|---|---|---|
| `ipv6_pool_cursors.pool_id` PK + FK | один cursor на pool, CASCADE | PK + FK CASCADE ✅ | OK |
| `ipv6_allocated_ips (pool_id, ip)` PK | один IP — один Address внутри pool | PK ✅ | OK |
| `ipv6_allocated_ips (pool_id, offset)` UNIQUE | один offset — один IP (interference-free allocate) | UNIQUE ✅ | OK |
| `ipv6_released_offsets (pool_id, offset)` PK | offset переиспользуется не более раза | PK ✅ + FK CASCADE | OK |
| pop released offset под concurrency | atomic | `FOR UPDATE SKIP LOCKED` ✅ | OK |

### 1.14 `vpc_outbox`, `fga_register_outbox`, `vpc_watch_cursors`, `operations`

| Table.field / invariant | Что гарантируется | DB constraint | Решение |
|---|---|---|---|
| `vpc_outbox.sequence_no` PK + sequence | строго возрастающий, уникальный | `PRIMARY KEY` + `nextval('vpc_outbox_sequence_no_seq')` default ✅ | OK |
| `vpc_outbox_notify_trg` AFTER INSERT | каждый INSERT → `pg_notify('vpc_outbox', sequence_no)` | trigger ✅ | OK |
| outbox row atomicity с ресурс-row | в одной tx | все `emitVPC` вызовы — в той же tx, что INSERT/UPDATE ресурса ✅ | OK |
| `fga_register_outbox` exactly-once claim | атомарный claim drainer'ом | `UPDATE … WHERE sent_at IS NULL AND attempt_count < $max FOR UPDATE SKIP LOCKED RETURNING …` ✅ | OK |
| `fga_register_outbox.event_type` | значение из enum | CHECK ✅ | OK |
| `vpc_watch_cursors.subscriber_id` PK | один cursor на subscriber | PK ✅ | OK |
| `operations.id` PK | уникальный | PK ✅ | OK |

---

## 2. Остаточные пункты

### G5 — Enum-like columns без CHECK constraints

**Severity**: Low — surface area для прямых INSERT-ов «мусора»; в текущем service-flow не достижимо.

**Затрагивает поля** с известным конечным набором valid values (enum из proto / domain), где
CHECK на DB-уровне пока не объявлен:

- `addresses.addr_type smallint`, `addresses.ip_version smallint`
- `gateways.gateway_type TEXT`
- `address_pools.kind smallint`

Service-слой всегда пишет валидное значение. Прямой `psql` UPDATE / dump-restore не из
текущей версии теоретически мог бы вставить мусор. NIC `status`, name/description/labels,
mac_address уже покрыты CHECK-ами в базовой схеме; SG `status` дропнут (миграция 0003).

**Решение**: при необходимости добавить CHECK новой миграцией `CHECK (col IN (…))`. Перед
apply — pre-flight `SELECT … WHERE NOT (…)` на стенде (constraint нагнется на невалидных
row'ах). При добавлении нового enum value в proto CHECK расширяется новой миграцией.

### G6 — `network_interfaces.security_group_ids` — within-service ref без DB/software enforcement

**Severity**: Medium — нарушение rule #10 (within-service ссылка обязана быть DB-выражена),
целостность firewall-политики без backstop.

`network_interfaces.security_group_ids jsonb` ссылается на `security_groups(id)` в ТОЙ ЖЕ БД
`kacho_vpc`, но связь не выражена НИ FK/join-table, НИ software-check'ом:

- NIC `Create`/`Update` пишут `security_group_ids` в domain без какой-либо проверки
  существования SG (use-case NIC не имеет `SecurityGroups`-reader-порта).
- `securitygroup/delete.go` гардит только `DefaultForNetwork`; `repo.Delete` безусловен —
  SG удаляется, даже если на него ссылается NIC → permanently dangling ref.

В отличие от `v4/v6_address_ids` (там backstop: `addresses.used` + `address_references` +
`AddressService.Delete` блокирует in-use address) — у `security_group_ids` backstop'а нет.

**Статус**: известный gap, трекается [PRO-Robotech/kacho-vpc#27](https://github.com/PRO-Robotech/kacho-vpc/issues/27);
persistent-RED newman-регрессия `SG-DEL-NEG-NIC-ATTACHED` (rule #13, «Known failing» в
`tests/newman/docs/RESULTS.md`) держит инвариант.

**Решение** (rule #10-корректное, отдельным behavioral-PR c APPROVED acceptance-доком):
join-table `network_interface_security_groups(nic_id → network_interfaces ON DELETE CASCADE,
sg_id → security_groups ON DELETE RESTRICT)` как source-of-truth (FK энфорсит существование +
блокирует `SG.Delete` пока референс жив; `23503` → FailedPrecondition), `security_group_ids`
jsonb остаётся output-only зеркалом. Меняет tenant-видимую семантику `SG.Delete` (теперь
может быть отклонён) → требует продуктового sign-off, вне scope contract-safe hardening-прохода.

---

## 3. Сводная таблица

| Категория | Field/invariant | Status |
|---|---|---|
| **Closed (DB-уровневое покрытие — OK)** | | |
| Все PK уникальности | все таблицы | ✅ |
| `(project_id, name)` UNIQUE по 7 ресурсам | networks/subnets/route_tables/security_groups/gateways/addresses/network_interfaces | ✅ |
| `subnets.network_id` FK ON DELETE RESTRICT | | ✅ |
| `subnets.route_table_id` FK ON DELETE SET NULL + auto-assoc triggers | | ✅ |
| Subnet CIDR overlap | EXCLUDE USING gist | ✅ |
| `addresses.internal_subnet_id` (generated, v4∪v6) FK RESTRICT | | ✅ |
| `route_tables.network_id` FK | | ✅ |
| `security_groups.network_id` FK ON DELETE RESTRICT (nullable) | | ✅ |
| Один default-SG на сеть | partial UNIQUE (0005) | ✅ |
| `networks.default_security_group_id` FK | ON DELETE SET NULL (0005) | ✅ |
| `network_interfaces.subnet_id` FK ON DELETE RESTRICT | | ✅ |
| NIC v4/v6 address cardinality ≤ 1 | CHECK | ✅ |
| NIC `used_by_id` attach race | atomic CAS | ✅ |
| NIC `mac_address` cloud-wide UNIQUE + формат | UNIQUE + CHECK | ✅ |
| Address external IP global UNIQUE (v4, v6) | partial UNIQUE | ✅ |
| Address internal IP per-subnet UNIQUE (v4, v6) | partial UNIQUE | ✅ |
| Address pool freelist atomic pop | `FOR UPDATE SKIP LOCKED` | ✅ |
| Address pool CIDR overlap per kind | EXCLUDE USING gist (0004) | ✅ |
| Address pool default per (zone, kind) | partial UNIQUE | ✅ |
| IPv6 cursor / allocated / released — atomic pop | `FOR UPDATE SKIP LOCKED` + UNIQUE | ✅ |
| SecurityGroup rules OCC | `xmin::text` CAS | ✅ |
| address_references attach race | CAS + PK backstop | ✅ |
| Outbox sequence + emit-в-той-же-tx | trigger + repo convention | ✅ |
| **Остаточные пункты** | | |
| Enum-like columns no CHECK | addr_type / ip_version / gateway_type / kind | **G5** (Low) |
| `security_group_ids` ref без FK/software-check | network_interfaces → security_groups | **G6** (Medium, [#27](https://github.com/PRO-Robotech/kacho-vpc/issues/27)) |

---

## 4. Регламент follow-up

- Каждый новый ссылочный field / инвариант: integration-тест обязателен (concurrent goroutines
  на спорный путь — ровно один winner, остальные получают ожидаемый sentinel). Эталоны:
  `internal/repo/network_interface_attach_race_integration_test.go` (atomic CAS),
  `internal/repo/kacho/pg/address.go` (`FOR UPDATE SKIP LOCKED`),
  `internal/repo/kacho/pg/security_group.go` (`xmin` OCC).
- Перед merge каждой миграции — pre-flight на dev-стенде: `SELECT … WHERE …` проверить, что
  existing rows не нарушают новый constraint; при найденных нарушениях — backfill в той же миграции.

---

## 5. Ссылки

- `05-database.md` — миграционная история, индексы, generated-columns.
- `er-diagram.md` — полная ER-схема и DB-level гарантии.
- `internal/migrations/0001_initial.sql..0009_operations_account_id.sql` — источник истины.
- `internal/repo/kacho/pg/network_interface.go` — эталон atomic CAS pattern.
- `internal/repo/kacho/pg/address.go` — эталон `FOR UPDATE SKIP LOCKED` pattern.
- `internal/repo/kacho/pg/security_group.go` — эталон `xmin` OCC pattern.
