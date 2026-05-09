# 05 — Database

`kacho_vpc` (`pg-vpc` StatefulSet в helm umbrella). Database-per-service —
никаких JOIN'ов с rm-БД или внешними источниками.

## Используемые продвинутые Postgres-фичи

| Фича | Где используется | Зачем |
|---|---|---|
| `EXCLUDE USING gist` | `subnets_no_overlap_v4/v6` (миграция 0007) | CIDR overlap rejection на DB-level (race-free) |
| `inet/cidr` operators (`<<`, `>>=`) | utilization counts (миграция 0022) | "сколько Address с IP внутри CIDR пула" |
| Partial UNIQUE index | `addresses_external_ip_uniq` WHERE address `<>` `''` (0017) | дубль external IP запретить, но empty allocate-pending разрешить |
| Partial UNIQUE index | `address_pools_zone_kind_default_uniq` WHERE is_default (0020) | один is_default=true на (zone, kind) |
| Computed column | `subnets.v4_cidr_primary` (0007), `addresses.internal_subnet_id` (0006) | для использования в EXCLUDE / UNIQUE |
| `JSONB` containment `@>` | `cloud_pool_selector` cascade Step 3 | match selector |
| `jsonb_path_ops` GIN index | `cloud_pool_selector_gin`, `address_pools_selector_labels_gin` | быстрые `@>` запросы |
| `LISTEN/NOTIFY` | `vpc_outbox_notify_trg` (0010) | InternalWatchService stream |
| `xmin::text` | optimistic locking (SecurityGroup.UpdateRules) | zero-overhead version-check |

## Миграции (полный список)

`internal/migrations/*.sql`, embed.FS, goose-стиль up/down.

| # | Файл | Что |
|---|---|---|
| 0001 | `operations.sql` | sync с corelib (operations table + sequence) |
| 0002 | `networks.sql` | базовая таблица Network |
| 0003 | `subnets.sql` | Subnet + FK на Network |
| 0004 | `addresses.sql` | Address + JSONB internal/external |
| 0005 | `route_tables.sql` | RouteTable + FK на Network |
| 0006 | `addresses_subnet_fk.sql` | computed `internal_subnet_id` для UNIQUE |
| 0007 | `subnets_cidr_exclude.sql` | **EXCLUDE USING gist** для CIDR overlap |
| 0008 | `security_groups.sql` | SecurityGroup + FK на Network |
| 0009 | `id_format_to_text.sql` | UUID → TEXT (verbatim YC ID format) |
| 0010 | `vpc_outbox.sql` | outbox + LISTEN/NOTIFY trigger |
| 0011 | `gateways.sql` | Gateway |
| 0012 | `private_endpoints.sql` | PrivateEndpoint |
| 0014 | `addresses_external_pool_id.sql` | Address.external_ipv4 расширен `address_pool_id` |
| 0015 | `address_pools.sql` | AddressPool + bindings (network/address) |
| 0016 | `address_pool_selectors.sql` | selector_labels + selector_priority + (был network_pool_selector) |
| 0017 | `addresses_external_ip_uniq_skip_empty.sql` | partial UNIQUE WHERE address `<>` `''` |
| 0018 | `networks_folder_name_unique.sql` | UNIQUE(folder_id, name) для Network |
| 0019 | `regions_zones.sql` | Region+Zone first-class + seed `ru-central1` |
| 0020 | `address_pools_zone.sql` | `region_id` (TEXT) → `zone_id` FK; partial UNIQUE на is_default |
| 0021 | `address_pools_drop_folder.sql` | AddressPool становится глобальным (drop folder_id) |
| 0022 | `pool_selector_to_cloud.sql` | drop network_pool_selector → cloud_pool_selector + UNIQUE(region_id, name) |

⚠️ Запреты:
- НЕ редактировать применённую миграцию. Только новая.
- НЕ изменять `0001_operations.sql` локально — синхронизируется из corelib.
- Нумерация может прыгать (0013 был removed). Это норма; смотрим на
  `goose_db_version` для актуального state.

## Ключевые таблицы

### `networks`

```
id                          TEXT PK (enp...)
folder_id                   TEXT NOT NULL
name                        TEXT NOT NULL
description, labels         TEXT, JSONB
default_security_group_id   TEXT NULL FK→security_groups ON DELETE SET NULL
created_at                  TIMESTAMPTZ

UNIQUE(folder_id, name)         (миграция 0018)
INDEX folder_idx
```

### `subnets`

```
id, folder_id, network_id (FK), zone_id        TEXT NOT NULL, immutable
name, description, labels
v4_cidr_blocks                 TEXT[] NOT NULL
v6_cidr_blocks                 TEXT[]
v4_cidr_primary                TEXT GENERATED ALWAYS AS (v4_cidr_blocks[1]) STORED
route_table_id                 TEXT NULL FK
dhcp_options                   JSONB

EXCLUDE USING gist (network_id WITH =, v4_cidr_primary inet_ops WITH &&)  -- 0007
EXCLUDE USING gist (network_id WITH =, v6_cidr_primary inet_ops WITH &&)
```

### `addresses`

```
id, folder_id                  TEXT NOT NULL
addr_type                      smallint  (1=ext, 2=int)
ip_version                     smallint
external_ipv4                  JSONB     (address, zone_id, address_pool_id, requirements)
internal_ipv4                  JSONB     (address, subnet_id)
internal_subnet_id             TEXT GENERATED (из internal_ipv4->>'subnet_id')  -- 0006
reserved, used                 BOOLEAN
deletion_protection            BOOLEAN

addresses_external_ip_uniq             UNIQUE (external_ipv4 ->> 'address') WHERE address <> ''  -- 0017
addresses_external_pool_ip_uniq        UNIQUE (external_ipv4 ->> 'address_pool_id', address)     -- 0015
addresses_internal_subnet_ip_uniq      UNIQUE (internal_subnet_id, internal_ipv4 ->> 'address')  -- 0006
```

### `address_pools`

```
id                      TEXT PK (apl...)
name, description, labels
cidr_blocks             TEXT[] NOT NULL
kind                    smallint
zone_id                 TEXT NULL FK→zones ON DELETE RESTRICT     -- 0020
is_default              BOOLEAN
selector_labels         JSONB
selector_priority       INT

address_pools_zone_kind_default_uniq    UNIQUE (COALESCE(zone_id, ''), kind) WHERE is_default
INDEX address_pools_zone_idx (zone_id)
GIN INDEX address_pools_selector_labels_gin (selector_labels jsonb_path_ops) WHERE selector_labels <> '{}'
```

### `regions`, `zones`

```
regions(id PK, name, created_at)                                  -- 0019
zones(id PK, region_id FK→regions RESTRICT, name, created_at)
UNIQUE INDEX zones_region_id_name_key (region_id, name) WHERE name <> ''    -- 0022
INDEX zones_region_idx
```

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
