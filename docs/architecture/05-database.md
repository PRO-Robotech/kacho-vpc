# 05 — Database

`kacho_vpc` (`pg-vpc` StatefulSet в helm umbrella). Database-per-service —
никаких JOIN'ов с чужими БД или внешними источниками. Все объекты (таблицы,
constraint, индексы, триггеры, helper-функции) живут в схеме `kacho_vpc`;
search_path устанавливается через libpq-параметр `options=-c search_path=kacho_vpc,public`.

## Используемые продвинутые Postgres-фичи

| Фича | Где используется | Зачем |
|---|---|---|
| `EXCLUDE USING gist` | `subnets_no_overlap_v4/v6`, `address_pool_cidrs` | CIDR overlap rejection на DB-level (race-free) |
| `inet/cidr` operators (`<<`, `>>=`) | utilization counts | "сколько Address с IP внутри CIDR пула" |
| Partial UNIQUE index | `addresses_external_ip_uniq` WHERE address `<>` `''` | дубль external IP запретить, но empty allocate-pending разрешить |
| Partial UNIQUE index | `<resource>_project_id_name_key` WHERE name `<>` `''` | дубль непустого `name` в project запретить, пустой — разрешить |
| Partial UNIQUE index | `address_pools_zone_kind_default_uniq` WHERE is_default | один is_default=true на (zone, kind) |
| Partial UNIQUE index | `security_groups_one_default_per_network` | один default-SG на сеть |
| Computed column | `subnets.v4_cidr_primary` / `v6_cidr_primary`, `addresses.internal_subnet_id` | для использования в EXCLUDE / UNIQUE / FK |
| `jsonb_path_ops` GIN index | `address_pools_selector_labels_gin` | быстрые `@>` запросы |
| `LISTEN/NOTIFY` | `vpc_outbox_notify_trg`, `fga_register_outbox_notify_trg` | in-cluster канал доменного outbox-журнала (Watch RPC не публикуется) + FGA register-drainer |
| `xmin::text` | optimistic locking (SecurityGroup.UpdateRules) | zero-overhead version-check |
| `FOR UPDATE SKIP LOCKED` | IPv4 freelist / IPv6 released-offsets pop | contention-free аллокация из пула |

## Миграции

`internal/migrations/*.sql`, embed.FS (объявлено в `migrations.go`), goose-стиль up/down.
`0001_initial.sql` — консолидированный baseline (вся базовая схема: все таблицы,
constraint inline в `CREATE TABLE`, индексы, EXCLUDE/UNIQUE, generated-колонки, триггеры,
helper-функции). Дальше — обычные инкрементные миграции:

| # | Файл | Что |
|---|---|---|
| 0001 | `0001_initial.sql` | базовая схема — `operations`, `networks`, `subnets`, `addresses`, `address_references`, `route_tables`, `security_groups`, `gateways`, `network_interfaces`, `vpc_outbox`, `vpc_watch_cursors`, `address_pools` + IPAM-таблицы; CHECK/FK/UNIQUE/EXCLUDE, generated columns, outbox/auto-assoc триггеры, `kacho_labels_valid`. Все id-колонки — `TEXT` |
| 0002 | `0002_drop_override_and_cloud_pool_selector.sql` | DROP `address_pool_address_override` + `cloud_pool_selector` — per-address override RPC и cloud-selector-шаг IPAM cascade упразднены |
| 0003 | `0003_drop_security_group_status.sql` | DROP `security_groups.status` — у SG нет provisioning-lifecycle, статус никем не наблюдался |
| 0004 | `0004_address_pool_cidrs.sql` | нормализованная child-таблица `address_pool_cidrs` + EXCLUDE gist — CIDR пулов не пересекаются per `kind` (declarative, race-free) |
| 0005 | `0005_default_sg_fk_and_unique.sql` | `networks.default_security_group_id` → nullable + FK ON DELETE SET NULL; partial UNIQUE `security_groups_one_default_per_network` |
| 0006 | `0006_fga_register_outbox.sql` | таблица `fga_register_outbox` (transactional-outbox для регистрации owner-tuple в FGA через kacho-iam) + LISTEN/NOTIFY-триггер |
| 0007 | `0007_network_vrf_id.sql` | `networks.vrf_id bigint` — sequence-backed уникальный per-network VRF id (инфра-чувствительное поле data-plane, отдается только через `InternalNetworkService.GetNetwork`) |
| 0008 | `0008_fga_register_outbox_resource_cols.sql` | additive `resource_kind` / `resource_id` на `fga_register_outbox` (нужны reconciler'у для адресации intent по ресурсу) |
| 0009 | `0009_operations_account_id.sql` | additive nullable `operations.account_id` (общий corelib LRO-writer INSERT'ит колонку безусловно) + partial index |

⚠️ Запреты:
- НЕ редактировать примененную миграцию. Только новая (следующая — `0010_*`).

## Ключевые таблицы

### `networks`

```
id                          TEXT PK (net...)
project_id                   TEXT NOT NULL
name                        TEXT NOT NULL
description, labels         TEXT, JSONB
default_security_group_id   TEXT NULL FK→security_groups ON DELETE SET NULL   -- 0005
vrf_id                      BIGINT UNIQUE NOT NULL    -- 0007; internal-only (InternalNetworkService)
created_at                  TIMESTAMPTZ

networks_project_id_name_key  UNIQUE (project_id, name)         -- non-partial (baseline)
INDEX project_idx
```

`vrf_id` — инфра-чувствительный per-network идентификатор data-plane: не публикуется на
public-поверхности, отдается только через `InternalNetworkService.GetNetwork`.

> Для остальных 5 ресурсов (`subnets`, `route_tables`, `security_groups`,
> `gateways`, `addresses`) UNIQUE на `(project_id, name)`
> — **partial**, `WHERE name <> ''`: пустой `name` допускает несколько ресурсов,
> дубль непустого → `23505` → `ALREADY_EXISTS`.

### `subnets`

```
id, project_id, network_id (FK), zone_id        TEXT NOT NULL, immutable    -- zone_id: plain TEXT, no FK (geography → kacho-geo)
name, description, labels
v4_cidr_blocks                 TEXT[] DEFAULT '{}'             -- опционально на Create
v6_cidr_blocks                 TEXT[]                          -- меняется через :add/:remove-cidr-blocks
v4_cidr_primary                CIDR GENERATED ALWAYS AS (v4_cidr_blocks[1]) STORED
v6_cidr_primary                CIDR GENERATED STORED
route_table_id                 TEXT NULL FK ON DELETE SET NULL
dhcp_options                   JSONB

subnets_project_id_name_key   UNIQUE (project_id, name) WHERE name <> ''
EXCLUDE USING gist (network_id WITH =, v4_cidr_primary inet_ops WITH &&)           -- subnets_no_overlap_v4
EXCLUDE USING gist (network_id WITH =, v6_cidr_primary inet_ops WITH &&)           -- subnets_no_overlap_v6
```

CIDR-less подсеть легальна; реальное добавление/удаление блоков (обеих семей) — через verbs
`:add-cidr-blocks` / `:remove-cidr-blocks`. Удаление подсети
блокируется, если у нее есть внутренние Address (v4 ИЛИ v6) или `NetworkInterface` — sync-precheck
в сервисе + DB-backstops `addresses_internal_subnet_fkey` / `network_interfaces_subnet_id_fkey`.

**Auto-association с RouteTable** (PL/pgSQL triggers):
- `rt_auto_assoc_subnets_trg` (AFTER INSERT ON route_tables) — выставляет `route_table_id` на subnets с `route_table_id IS NULL` в той же сети.
- `subnet_auto_pick_rt_trg` (BEFORE INSERT ON subnets) — заполняет `NEW.route_table_id` самой ранней RT этой сети, если клиент не задал.
- `subnets_outbox_emit_route_table_change_trg` (AFTER UPDATE OF route_table_id) — эмитит `Subnet.UPDATED` в `vpc_outbox`.

### `addresses`

```
id, project_id                  TEXT NOT NULL
addr_type                      smallint  (1=ext, 2=int)
ip_version                     smallint
external_ipv4                  JSONB     (address, zone_id, address_pool_id, requirements)
external_ipv6                  JSONB
internal_ipv4                  JSONB     (address, subnet_id)
internal_ipv6                  JSONB     (address, subnet_id)
internal_subnet_id             TEXT GENERATED (из internal_ipv4->>'subnet_id' ИЛИ internal_ipv6->>'subnet_id')
reserved, used                 BOOLEAN                                           -- used=true ⇔ есть referrer-row
used_by                        (flat used_by_type/used_by_id/used_by_name)
deletion_protection            BOOLEAN

addresses_project_id_name_key           UNIQUE (project_id, name) WHERE name <> ''
addresses_external_ip_uniq             UNIQUE (external_ipv4 ->> 'address') WHERE address <> ''
addresses_external_pool_ip_uniq        UNIQUE (external_ipv4 ->> 'address_pool_id', address)
addresses_external_v6_pool_ip_uniq     UNIQUE (external_ipv6 ->> 'address_pool_id', address)
addresses_internal_subnet_ip_uniq      UNIQUE (internal_subnet_id, internal_ipv4 ->> 'address')
addresses_internal_subnet_ipv6_uniq    UNIQUE ((internal_ipv6 ->> 'subnet_id'), (internal_ipv6 ->> 'address'))
addresses_internal_subnet_fkey         FK (internal_subnet_id) → subnets(id) ON DELETE RESTRICT  -- generated col покрывает v4+v6
```

`Address.Delete` блокируется, если адрес `used` (referrer = `NetworkInterface`) →
`FailedPrecondition "address ... is in use by network interface ...; detach it before deleting the address"`.

### `network_interfaces`

First-class самостоятельный сетевой интерфейс (NIC). Project-level, принадлежит `Subnet`.

```
id                  TEXT PK (nic...)
project_id           TEXT NOT NULL
name, labels
subnet_id           TEXT NOT NULL FK→subnets(id) ON DELETE RESTRICT
v4_address_ids      JSONB   -- ссылки на Address по id; один Address ≤ на одном NIC; CHECK jsonb_array_length<=1
v6_address_ids      JSONB   -- CHECK jsonb_array_length<=1
security_group_ids  JSONB   -- default на Create = Network.default_security_group_id сети подсети; network-less SG ок (тот же project)
used_by_type / used_by_id / used_by_name   TEXT   -- denormalised Reference «кто использует NIC» — устанавливается атомарным CAS на смену владельца
mac_address         TEXT UNIQUE cloud-wide, NOT NULL    -- output-only, аллоцируется при Create
status              TEXT  -- PROVISIONING/ACTIVE/AVAILABLE/FAILED/DELETING
created_at          TIMESTAMPTZ
```

Может быть создан без адресов. Проекция чисто control-plane (lean) — инфра-полей у
kacho-vpc нет.

### `security_groups` (NULLABLE `network_id`)

`security_groups.network_id` — nullable: project-level (network-less) SG легальна; пустой
`network_id` в домене хранится как SQL `NULL`, чтобы FK `security_groups_network_id_fkey`
(ON DELETE RESTRICT) не срабатывал на `''`. `List?filter=network_id="<id>"` работает
(`network_id` в whitelist фильтра); default-SG-на-сети всегда ставит непустой `network_id`.
Один default-SG на сеть гарантируется partial UNIQUE `security_groups_one_default_per_network`.

### `address_pools`

```
id                      TEXT PK (apl...)
name, description, labels
v4_cidr_blocks          TEXT[]
v6_cidr_blocks          TEXT[]
kind                    smallint
zone_id                 TEXT NULL                                 -- plain TEXT, no FK (zones → kacho-geo); NULL = глобальный fallback
is_default              BOOLEAN
selector_labels         JSONB
selector_priority       INT

address_pools_zone_kind_default_uniq    UNIQUE (COALESCE(zone_id, ''), kind) WHERE is_default
GIN INDEX address_pools_selector_labels_gin (selector_labels jsonb_path_ops) WHERE selector_labels <> '{}'
```

CIDR пулов не пересекаются per `kind` — через нормализованную child-таблицу
`address_pool_cidrs` + EXCLUDE gist (миграция 0004).

### Geography (Region/Zone) — не в kacho-vpc

Geography (Region/Zone) — домен leaf-сервиса `kacho-geo`.
В `kacho-vpc` этих таблиц нет; `subnets.zone_id` / `address_pools.zone_id` /
`addresses.external_ipv4->>'zone_id'` — просто `TEXT`-id без FK, существование валидируется на
request-path через `geo.v1.ZoneService.Get`; dangling-ref (зона удалена в kacho-geo) переживается
грациозно на чтении.

### `address_pool_network_default`

```
address_pool_network_default(network_id PK FK→networks ON DELETE CASCADE, pool_id FK→address_pools ON DELETE RESTRICT)
```

### `address_references`

Referrer-tracking «кто использует адрес». Один referrer на адрес.

```
address_id     TEXT  PK  FK→addresses ON DELETE CASCADE
referrer_type  TEXT      ("compute_instance" | "network_interface" — расширяемо)
referrer_id    TEXT      (id ресурса-владельца — id ВМ / id NIC)
referrer_name  TEXT      ('' если не задано; best-effort на момент привязки)
attached_at    TIMESTAMPTZ DEFAULT now

index address_references_referrer_idx (referrer_type, referrer_id)
```

`addresses.used` поддерживается сервис-слоем синхронно: `true` ⇔ существует
referrer-row (SetReference выставляет, ClearReference снимает; FK CASCADE
убирает row при удалении адреса). Управляется через
`InternalAddressService.{Set,Clear,Get}AddressReference`; surfaced через
`SubnetService.ListUsedAddresses` (`UsedAddress.references[]`). `NetworkInterface.Create`
с `v4_address_ids[]`/`v6_address_ids[]` ставит referrer-rows `referrer_type="network_interface"`
(один Address ≤ на одном NIC); `Address.Delete` для `used`-адреса → `FailedPrecondition`.
kacho-compute привязывает эфемерные NIC-адреса ВМ через эти RPC.

### `vpc_outbox`

```
sequence_no       BIGINT PK  DEFAULT nextval(vpc_outbox_sequence_no_seq)
resource_kind     TEXT
resource_id       TEXT
event_type        TEXT  (CREATED|UPDATED|DELETED)
payload           JSONB
created_at        TIMESTAMPTZ DEFAULT now
processed_at      TIMESTAMPTZ

trigger vpc_outbox_notify_trg AFTER INSERT
  EXECUTE PROCEDURE pg_notify('vpc_outbox', NEW.sequence_no::text)
```

### `fga_register_outbox` (миграция 0006/0008)

Transactional-outbox для регистрации owner-tuple в FGA через `kacho-iam`. Намерение
«register/unregister owner-tuple» пишется строкой в той же writer-TX, что вставляет/удаляет
ресурс (один commit, без dual-write); отдельный register-drainer применяет каждое намерение
через `InternalIAMService.RegisterResource`/`Unregister`. LISTEN/NOTIFY-канал
`kacho_vpc_fga_register_outbox` будит drainer на INSERT.

### `operations`

Из corelib `migrations/common`. PK `id` (`enp...`). Без FK на ресурсы (resource может быть
удален до завершения op). `account_id` — nullable денормализация (миграция 0009; для vpc
остается NULL).

## Connection / pooling

- `kacho-corelib/db.NewPool(cfg)` — pgxpool с retry + lifecycle.
- `KACHO_VPC_DB_MAX_CONNS` прокидывается в DSN (`pool_max_conns`) **только** для pgxpool;
  `migrate` использует отдельный `MigrateDSN` без этого параметра (иначе `database/sql`
  шлет серверу неизвестный PG-параметр → `FATAL`).
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
  unnest(ap.v4_cidr_blocks) AS cidr,
  count(*) FILTER (WHERE a.external_ipv4 IS NOT NULL) AS used
FROM address_pools ap
LEFT JOIN addresses a
  ON a.external_ipv4 ->> 'address_pool_id' = ap.id
GROUP BY ap.id, ap.name, ap.zone_id, ap.v4_cidr_blocks;

-- Найти dangling Address (no allocated IP старше 5 минут)
SELECT id, project_id, name, external_ipv4, created_at
FROM addresses
WHERE external_ipv4 IS NOT NULL
  AND coalesce(external_ipv4 ->> 'address', '') = ''
  AND created_at < now() - interval '5 minutes';
```
