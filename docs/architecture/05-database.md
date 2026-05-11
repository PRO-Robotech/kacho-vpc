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
| Computed column | `subnets.v4_cidr_primary`, `addresses.internal_subnet_id` | для использования в EXCLUDE / UNIQUE |
| `JSONB` containment `@>` | `cloud_pool_selector` cascade Step 3 | match selector |
| `jsonb_path_ops` GIN index | `cloud_pool_selector_gin`, `address_pools_selector_labels_gin` | быстрые `@>` запросы |
| `LISTEN/NOTIFY` | `vpc_outbox_notify_trg` | InternalWatchService stream |
| `xmin::text` | optimistic locking (SecurityGroup.UpdateRules) | zero-overhead version-check |

## Миграции

`internal/migrations/*.sql`, embed.FS (объявлено в `migrations.go`), goose-стиль up/down.
**Физически два файла** — 22 исторические миграции свёрнуты (commit `5581316`,
`refactor(vpc): inline AddressAllocator + squash 22 миграции`) в один baseline:

| # | Файл | Что |
|---|---|---|
| 0001 | `0001_initial.sql` | **squashed baseline** — все таблицы (`operations`, `networks`, `subnets`, `addresses`, `route_tables`, `security_groups`, `gateways`, `private_endpoints`, `regions`, `zones`, `address_pools`, binding-таблицы, `cloud_pool_selector`, `vpc_outbox`, `vpc_watch_cursors`), индексы, EXCLUDE/UNIQUE constraints, generated columns, outbox trigger. Id-колонки — `TEXT`. `networks_folder_id_name_key` — non-partial UNIQUE `(folder_id, name)` |
| 0002 | `0002_resource_name_unique.sql` | partial UNIQUE `(folder_id, name) WHERE name <> ''` для `subnets`/`route_tables`/`security_groups`/`gateways`/`private_endpoints`/`addresses` (закрыл расхождение с verbatim YC — раньше UNIQUE был только у Network; commit `ee07a7e`) |

`migrations/` в корне репо — staging для `make sync-migrations` (только
`0001_operations.sql` от corelib; источник истины не здесь, в `0001_initial.sql`
схема `operations` уже включена).

> Историческая нумерация (0001–0022 до squash) встречается ниже в схемах
> как «(миграция 0007)» и т.п. — это происхождение конкретного DDL,
> физически всё в `0001_initial.sql`. Актуальный state БД — в `goose_db_version`.

⚠️ Запреты:
- НЕ редактировать применённую миграцию (`0001_initial.sql`, `0002_*`). Только новая.
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
created_at                  TIMESTAMPTZ

networks_folder_id_name_key  UNIQUE (folder_id, name)         -- non-partial (в baseline)
INDEX folder_idx
```

> Для остальных 6 ресурсов (`subnets`, `route_tables`, `security_groups`,
> `gateways`, `private_endpoints`, `addresses`) UNIQUE на `(folder_id, name)`
> — **partial**, `WHERE name <> ''` (миграция `0002_resource_name_unique.sql`):
> пустой `name` допускает несколько ресурсов (verbatim YC permissive policy),
> дубль непустого → `23505` → `ALREADY_EXISTS`.

### `subnets`

```
id, folder_id, network_id (FK), zone_id        TEXT NOT NULL, immutable
name, description, labels
v4_cidr_blocks                 TEXT[] NOT NULL
v6_cidr_blocks                 TEXT[]
v4_cidr_primary                TEXT GENERATED ALWAYS AS (v4_cidr_blocks[1]) STORED
route_table_id                 TEXT NULL FK
dhcp_options                   JSONB

subnets_folder_id_name_key   UNIQUE (folder_id, name) WHERE name <> ''             -- 0002
EXCLUDE USING gist (network_id WITH =, v4_cidr_primary inet_ops WITH &&)           -- subnets_no_overlap_v4
EXCLUDE USING gist (network_id WITH =, v6_cidr_primary inet_ops WITH &&)           -- subnets_no_overlap_v6
```

### `addresses`

```
id, folder_id                  TEXT NOT NULL
addr_type                      smallint  (1=ext, 2=int)
ip_version                     smallint
external_ipv4                  JSONB     (address, zone_id, address_pool_id, requirements)
internal_ipv4                  JSONB     (address, subnet_id)
internal_subnet_id             TEXT GENERATED (из internal_ipv4->>'subnet_id')
reserved, used                 BOOLEAN
deletion_protection            BOOLEAN

addresses_folder_id_name_key           UNIQUE (folder_id, name) WHERE name <> ''                 -- 0002
addresses_external_ip_uniq             UNIQUE (external_ipv4 ->> 'address') WHERE address <> ''
addresses_external_pool_ip_uniq        UNIQUE (external_ipv4 ->> 'address_pool_id', address)
addresses_internal_subnet_ip_uniq      UNIQUE (internal_subnet_id, internal_ipv4 ->> 'address')
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

### `address_references` (миграция 0003)

Referrer-tracking «кто использует адрес» (YC-like). Один referrer на адрес.

```
address_id     TEXT  PK  FK→addresses ON DELETE CASCADE
referrer_type  TEXT      ("compute_instance" — расширяемо)
referrer_id    TEXT      (id ресурса-владельца, напр. id ВМ)
referrer_name  TEXT      ('' если не задано; best-effort на момент привязки)
attached_at    TIMESTAMPTZ DEFAULT now()

index address_references_referrer_idx (referrer_type, referrer_id)
```

`addresses.used` поддерживается сервис-слоем синхронно: `true` ⇔ существует
referrer-row (SetReference выставляет, ClearReference снимает; FK CASCADE
убирает row при удалении адреса). Управляется через
`InternalAddressService.{Set,Clear,Get}AddressReference`; surfaced через
`SubnetService.ListUsedAddresses` (`UsedAddress.references[]`). kacho-compute
привязывает эфемерные NIC-адреса ВМ через эти RPC.

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
