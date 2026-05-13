# 05 — Database

`kacho_vpc` (`pg-vpc` StatefulSet в helm umbrella). Database-per-service —
никаких JOIN'ов с rm-БД или внешними источниками.

## Используемые продвинутые Postgres-фичи

| Фича | Где используется | Зачем |
|---|---|---|
| `EXCLUDE USING gist` | `subnets_no_overlap_v4/v6` | CIDR overlap rejection на DB-level (race-free) |
| `inet/cidr` operators (`<<`, `>>=`) | utilization counts | "сколько Address с IP внутри CIDR пула" |
| Partial UNIQUE index | `addresses_external_ip_uniq` WHERE address `<>` `''` | дубль external IP запретить, но empty allocate-pending разрешить |
| Partial UNIQUE index | `<resource>_folder_id_name_key` WHERE name `<>` `''` (миграция `0002`) | дубль непустого `name` в folder запретить, пустой — разрешить |
| Partial UNIQUE index | `address_pools_zone_kind_default_uniq` WHERE is_default | один is_default=true на (zone, kind) |
| Computed column | `subnets.v4_cidr_primary`, `addresses.internal_subnet_id` (выводится из `internal_ipv4` ИЛИ `internal_ipv6` — миграция 0013) | для использования в EXCLUDE / UNIQUE / FK |
| `SEQUENCE` + free-list таблица | `vpn_id_seq` + `vpn_id_free` | аллокация/реюз 24-bit `networks.vpn_id` (миграция 0005, internal-only) |
| `JSONB` containment `@>` | `cloud_pool_selector` cascade Step 3 | match selector |
| `jsonb_path_ops` GIN index | `cloud_pool_selector_gin`, `address_pools_selector_labels_gin` | быстрые `@>` запросы |
| `LISTEN/NOTIFY` | `vpc_outbox_notify_trg` | InternalWatchService stream |
| `xmin::text` | optimistic locking (SecurityGroup.UpdateRules) | zero-overhead version-check |

## Миграции

`internal/migrations/*.sql`, embed.FS (объявлено в `migrations.go`), goose-стиль up/down.
Baseline (`0001_initial.sql`) свернул 22 исторические миграции (commit `5581316`,
`refactor(vpc): inline AddressAllocator + squash 22 миграции`); дальше — обычные
инкрементные миграции:

| # | Файл | Что |
|---|---|---|
| 0001 | `0001_initial.sql` | **squashed baseline** — все таблицы (`operations`, `networks`, `subnets`, `addresses`, `route_tables`, `security_groups`, `gateways`, `private_endpoints`, `regions`, `zones`, `address_pools`, binding-таблицы, `cloud_pool_selector`, `vpc_outbox`, `vpc_watch_cursors`), индексы, EXCLUDE/UNIQUE constraints, generated columns, outbox trigger. Id-колонки — `TEXT`. `networks_folder_id_name_key` — non-partial UNIQUE `(folder_id, name)` |
| 0002 | `0002_resource_name_unique.sql` | partial UNIQUE `(folder_id, name) WHERE name <> ''` для `subnets`/`route_tables`/`security_groups`/`gateways`/`private_endpoints`/`addresses` (раньше UNIQUE был только у Network; commit `ee07a7e`) |
| 0003 | `0003_address_references.sql` | таблица `address_references` (referrer-tracking «кто использует адрес»; см. ниже) |
| 0004 | `0004_drop_geography.sql` | DROP `zones`, `regions` — Geography (Region/Zone) переехала в `kacho-compute` (эпик KAC-15); `zone_id`-колонки остаются `TEXT` без FK, валидируются через `compute.v1.ZoneService.Get` |
| 0005 | `0005_network_vpn_id.sql` | `networks.vpn_id integer NOT NULL UNIQUE` (24-bit data-plane-id, epic KAC-2) + `SEQUENCE vpn_id_seq` (1..16777215) + free-list `vpn_id_free(id integer PK)`; **internal-only** (отдаётся только через `InternalNetworkService.GetNetwork`) |
| 0006 | `0006_network_interfaces.sql` | таблица `network_interfaces` (first-class NIC; см. ниже) |
| 0007 | `0007_nic_address_refs.sql` | NIC ↔ Address ссылки по id (`v4_address_ids[]`/`v6_address_ids[]`) + referrer-rows `referrer_type="network_interface"` |
| 0008 | `0008_nic_restructure.sql` | реструктуризация NIC → `used_by` (flat-колонки `used_by_type`/`used_by_id`/`used_by_name`) |
| 0009 | `0009_address_internal_ipv6.sql` | `addresses.internal_ipv6 jsonb` + partial UNIQUE `addresses_internal_subnet_ipv6_uniq` на `((internal_ipv6->>'subnet_id'), (internal_ipv6->>'address'))` |
| 0010 | `0010_optional_sg_network.sql` | `security_groups.network_id DROP NOT NULL` — folder-level (network-less) SG легальна; пустой `network_id` хранится как SQL `NULL` |
| 0011 | `0011_nic_subnet_cascade.sql` | `network_interfaces_subnet_id_fkey` RESTRICT → CASCADE — **superseded** (откачено ниже) |
| 0012 | `0012_nic_subnet_restrict.sql` | откат 0011: `network_interfaces_subnet_id_fkey` обратно `ON DELETE RESTRICT` — NIC всегда блокирует свою подсеть |
| 0013 | `0013_address_internal_subnet_id_v6.sql` | PG16-compatible drop/recreate generated-колонки `addresses.internal_subnet_id` — теперь выводится из `internal_ipv4->>'subnet_id'` **ИЛИ** `internal_ipv6->>'subnet_id'` (чтобы и v4-, и v6-internal-адрес блокировал свою подсеть через FK `addresses_internal_subnet_fkey`); миграция заодно вычищает pre-existing dangling internal-subnet refs перед re-add'ом FK |

`migrations/` в корне репо — staging для `make sync-migrations` (только
`0001_operations.sql` от corelib; источник истины не здесь, в `0001_initial.sql`
схема `operations` уже включена).

> Историческая нумерация (0001–0022 *внутри* squash) встречается ниже в схемах
> как «(миграция 0007)» и т.п. — это происхождение конкретного DDL внутри
> `0001_initial.sql`; не путать с физическими файлами 0001–0013 выше.
> Актуальный state БД — в `goose_db_version`.

⚠️ Запреты:
- НЕ редактировать применённую миграцию (`0001_initial.sql` … `0013_*`). Только новая (следующая — `0014_*`).
- НЕ изменять `0001_operations.sql` (staging-копия из corelib) — `0001_initial.sql`
  уже содержит схему `operations`; `make sync-migrations` синхронизирует staging-копию.

## Ключевые таблицы

### `networks`

```
id                          TEXT PK (enp...)
folder_id                   TEXT NOT NULL
name                        TEXT NOT NULL
description, labels         TEXT, JSONB
default_security_group_id   TEXT NULL FK→security_groups ON DELETE SET NULL
vpn_id                      INTEGER NOT NULL                  -- 0005; 24-bit data-plane-id, INTERNAL-ONLY
created_at                  TIMESTAMPTZ

networks_folder_id_name_key  UNIQUE (folder_id, name)         -- non-partial (в baseline)
networks_vpn_id_key          UNIQUE (vpn_id)                  -- 0005
INDEX folder_idx
```

`vpn_id` аллоцируется на Network.Create (минимальный из `vpn_id_free`, иначе `nextval(vpn_id_seq)`),
стабилен, на Network.Delete возвращается в `vpn_id_free`. **Не в публичном `Network` API** — только
`InternalNetworkService.GetNetwork → InternalNetwork{network, vpn_id}` / REST `GET /vpc/v1/networks/{id}/internal`
(internal mux). Сопутствующие объекты (0005):

```
SEQUENCE vpn_id_seq START 1 MINVALUE 1 MAXVALUE 16777215
TABLE    vpn_id_free (id INTEGER PRIMARY KEY)                 -- free-list освобождённых vpn_id
```

> Для остальных 6 ресурсов (`subnets`, `route_tables`, `security_groups`,
> `gateways`, `private_endpoints`, `addresses`) UNIQUE на `(folder_id, name)`
> — **partial**, `WHERE name <> ''` (миграция `0002_resource_name_unique.sql`):
> пустой `name` допускает несколько ресурсов (verbatim YC permissive policy),
> дубль непустого → `23505` → `ALREADY_EXISTS`.

### `subnets`

```
id, folder_id, network_id (FK), zone_id        TEXT NOT NULL, immutable    -- zone_id: plain TEXT, no FK (geography → kacho-compute)
name, description, labels
v4_cidr_blocks                 TEXT[] DEFAULT '{}'             -- опционально на Create (proto-(required) снят)
v6_cidr_blocks                 JSONB                           -- меняется через :add/:remove-cidr-blocks
v4_cidr_primary                TEXT GENERATED ALWAYS AS (v4_cidr_blocks[1]) STORED
route_table_id                 TEXT NULL FK
dhcp_options                   JSONB

subnets_folder_id_name_key   UNIQUE (folder_id, name) WHERE name <> ''             -- 0002
EXCLUDE USING gist (network_id WITH =, v4_cidr_primary inet_ops WITH &&)           -- subnets_no_overlap_v4
EXCLUDE USING gist (network_id WITH =, v6_cidr_primary inet_ops WITH &&)           -- subnets_no_overlap_v6 (cross-subnet backstop для v6-блоков)
```

CIDR-less подсеть легальна; реальное добавление/удаление блоков (обеих семей) — через verbs
`:add-cidr-blocks` / `:remove-cidr-blocks` (теперь принимают и `v6_cidr_blocks`). Удаление подсети
блокируется, если у неё есть внутренние Address (v4 ИЛИ v6) или `NetworkInterface` — sync-precheck
в сервисе + DB-backstops `addresses_internal_subnet_fkey` / `network_interfaces_subnet_id_fkey`.

### `addresses`

```
id, folder_id                  TEXT NOT NULL
addr_type                      smallint  (1=ext, 2=int)
ip_version                     smallint
external_ipv4                  JSONB     (address, zone_id, address_pool_id, requirements)
internal_ipv4                  JSONB     (address, subnet_id)
internal_ipv6                  JSONB     (address, subnet_id)                    -- 0009
internal_subnet_id             TEXT GENERATED (из internal_ipv4->>'subnet_id' ИЛИ internal_ipv6->>'subnet_id')  -- 0013 (PG16 drop/recreate)
reserved, used                 BOOLEAN                                           -- used=true ⇔ есть referrer-row
used_by                        (flat used_by_type/used_by_id/used_by_name)
deletion_protection            BOOLEAN

addresses_folder_id_name_key           UNIQUE (folder_id, name) WHERE name <> ''                 -- 0002
addresses_external_ip_uniq             UNIQUE (external_ipv4 ->> 'address') WHERE address <> ''
addresses_external_pool_ip_uniq        UNIQUE (external_ipv4 ->> 'address_pool_id', address)
addresses_internal_subnet_ip_uniq      UNIQUE (internal_subnet_id, internal_ipv4 ->> 'address')
addresses_internal_subnet_ipv6_uniq    UNIQUE ((internal_ipv6 ->> 'subnet_id'), (internal_ipv6 ->> 'address'))  -- 0009; partial; conflict-target для AllocateInternalIPv6
addresses_internal_subnet_fkey         FK (internal_subnet_id) → subnets(id) ON DELETE RESTRICT  -- generated col покрывает v4+v6 с 0013
```

`Address.Delete` блокируется, если адрес `used` (referrer = `NetworkInterface`) →
`FailedPrecondition "address ... is in use by network interface ...; detach it before deleting the address"` (KAC-31).

### `network_interfaces` (миграции 0006/0007/0008; эпик KAC-2)

First-class AWS-ENI-подобный ресурс (NIC). Folder-level, принадлежит `Subnet`.

```
id                  TEXT PK (e9b... — переиспользует ids.PrefixSubnet, отдельного PrefixNetworkInterface нет)
folder_id           TEXT NOT NULL
name, labels
subnet_id           TEXT NOT NULL FK→subnets(id) ON DELETE RESTRICT      -- network_interfaces_subnet_id_fkey (миграция 0012 откатила KAC-31's CASCADE из 0011)
v4_address_ids      TEXT[]   -- ссылки на Address по id; один Address ≤ на одном NIC (referrer-rows address_references, referrer_type="network_interface")
v6_address_ids      TEXT[]
security_group_ids  TEXT[]   -- default на Create = Network.default_security_group_id сети подсети; network-less SG ок (тот же folder)
used_by_type / used_by_id / used_by_name   TEXT   -- denormalised Reference «кто приаттачил» — выставляет AttachToInstance, чистит DetachFromInstance
status              smallint  -- PROVISIONING/ACTIVE/AVAILABLE/FAILED/DELETING
created_at          TIMESTAMPTZ
```

Может быть создан без адресов. Публичная проекция lean; data-plane-инфа (resolved `vpn_id`, `hv_id` placement,
`sid`/`sid_seq`, `host_iface`, `netns`, `gateway_ip`, `container_id`, `status_error`, `dataplane_revision`,
resolved v4/v6 address strings) — `InternalNetworkInterface`, заполняется `kacho-vpc-implement` через `ReportNiDataplane`.

### `security_groups` (NULLABLE `network_id` с 0010)

`security_groups.network_id` — `DROP NOT NULL` (миграция `0010_optional_sg_network.sql`): folder-level
(network-less) SG легальна; пустой `network_id` в домене хранится как SQL `NULL`, чтобы FK
`security_groups_network_id_fkey` не срабатывал на `''`. `List?filter=network_id="<id>"` работает
(`network_id` в whitelist фильтра); default-SG-на-сети всегда ставит непустой `network_id`.

### `address_pools`

```
id                      TEXT PK (apl...)
name, description, labels
cidr_blocks             TEXT[] NOT NULL
kind                    smallint
zone_id                 TEXT NULL                                 -- plain TEXT, no FK (zones → kacho-compute, миграция 0004); NULL = глобальный fallback
is_default              BOOLEAN
selector_labels         JSONB
selector_priority       INT

address_pools_zone_kind_default_uniq    UNIQUE (COALESCE(zone_id, ''), kind) WHERE is_default
INDEX address_pools_zone_idx (zone_id)
GIN INDEX address_pools_selector_labels_gin (selector_labels jsonb_path_ops) WHERE selector_labels <> '{}'
```

### ~~`regions`, `zones`~~ — удалены (миграция `0004_drop_geography.sql`)

Geography (Region/Zone) переехала в `kacho-compute` (эпик KAC-15). В `kacho-vpc` этих таблиц нет;
`subnets.zone_id` / `address_pools.zone_id` / `addresses.external_ipv4->>'zone_id'` — просто `TEXT`-id
без FK, существование валидируется на request-path через `compute.v1.ZoneService.Get`; dangling-ref
(зона удалена в kacho-compute) переживается грациозно на чтении.

### `cloud_pool_selector`

```
cloud_id                TEXT PK             -- foreign на rm.clouds, no DB FK
selector                JSONB
set_at, set_by

GIN INDEX cloud_pool_selector_gin (selector jsonb_path_ops) WHERE selector <> '{}'
```

### `address_pool_network_default`, `address_pool_address_override`

```
address_pool_network_default(network_id PK, pool_id FK→address_pools)
address_pool_address_override(address_id PK, pool_id FK→address_pools)
```

### `address_references` (миграция 0003)

Referrer-tracking «кто использует адрес» (YC-like). Один referrer на адрес.

```
address_id     TEXT  PK  FK→addresses ON DELETE CASCADE
referrer_type  TEXT      ("compute_instance" | "network_interface" — расширяемо)
referrer_id    TEXT      (id ресурса-владельца — id ВМ / id NIC)
referrer_name  TEXT      ('' если не задано; best-effort на момент привязки)
attached_at    TIMESTAMPTZ DEFAULT now()

index address_references_referrer_idx (referrer_type, referrer_id)
```

`addresses.used` поддерживается сервис-слоем синхронно: `true` ⇔ существует
referrer-row (SetReference выставляет, ClearReference снимает; FK CASCADE
убирает row при удалении адреса). Управляется через
`InternalAddressService.{Set,Clear,Get}AddressReference`; surfaced через
`SubnetService.ListUsedAddresses` (`UsedAddress.references[]`). `NetworkInterface.Create`
с `v4_address_ids[]`/`v6_address_ids[]` ставит referrer-rows `referrer_type="network_interface"`
(один Address ≤ на одном NIC); `Address.Delete` для `used`-адреса → `FailedPrecondition` (KAC-31).
kacho-compute привязывает эфемерные NIC-адреса ВМ через эти RPC.

### `vpc_outbox`

```
sequence_no       BIGSERIAL PK
resource_type     TEXT
resource_id       TEXT
op                TEXT  (CREATED|UPDATED|DELETED)
payload           JSONB
created_at        TIMESTAMPTZ DEFAULT now()

trigger vpc_outbox_notify_trg AFTER INSERT
  EXECUTE PROCEDURE pg_notify('vpc_outbox', NEW.sequence_no::text)
```

### `operations`

Из corelib `migrations/common`. Не редактируем локально.

## Connection / pooling

- `kacho-corelib/db.NewPool(cfg)` — pgxpool с retry + lifecycle.
- Default pool size — настраивается через `KACHO_VPC_DB_*` env vars.
- Init container `migrate up` прокатывает миграции до старта основного.

## psql быстрый доступ

```bash
# Из kacho-deploy
make psql SVC=vpc

# Эквивалент:
kubectl exec -n kacho kacho-umbrella-pg-vpc-0 -- env PGPASSWORD=dev-vpc-password \
  psql -U vpc -d kacho_vpc
```

Полезные команды:

```sql
-- Список всех миграций
SELECT * FROM goose_db_version ORDER BY version_id DESC LIMIT 10;

-- Все индексы по таблице
\d address_pools
\d addresses

-- Pool utilization вручную
SELECT
  ap.name, ap.zone_id,
  unnest(ap.cidr_blocks) AS cidr,
  count(*) FILTER (WHERE a.external_ipv4 IS NOT NULL) AS used
FROM address_pools ap
LEFT JOIN addresses a
  ON a.external_ipv4 ->> 'address_pool_id' = ap.id
GROUP BY ap.id, ap.name, ap.zone_id, ap.cidr_blocks;

-- Найти dangling Address (no allocated IP старше 5 минут)
SELECT id, folder_id, name, external_ipv4, created_at
FROM addresses
WHERE external_ipv4 IS NOT NULL
  AND coalesce(external_ipv4 ->> 'address', '') = ''
  AND created_at < now() - interval '5 minutes';
```
