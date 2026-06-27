# `kacho-vpc` migrations — правила написания

Этот документ — нормативный регламент для **новых** миграций в `internal/migrations/`.
Базовое правило: constraint / index / FK живут близко к declaration таблицы — в той
же миграции, что и `CREATE TABLE`, не в следующей. Все правила ниже — **обязательные**.

Сопутствующий контекст:

- Миграции embedded через `embed.FS`, применяются goose; DSN — `MigrateDSN`.
- Схема сервиса — `kacho_vpc`; весь DDL schema-qualified, search-path задается через
  libpq `options`.
- БД — «последний рубеж» валидации: domain-уровень дает fast-fail для пользователя, но
  каждое ограничение дублируется DB-конструкцией.
- Изменение схемы, требующее backfill существующих строк, оформляется через
  `ADD CONSTRAINT … NOT VALID` + `VALIDATE CONSTRAINT` (двухфазный workflow, чтобы не
  блокировать таблицу).

> **TL;DR:** новая таблица — все ее CHECK / FK / UNIQUE / EXCLUDE / index в **том же**
> файле миграции, где `CREATE TABLE`. Никаких post-hoc `ALTER TABLE ADD CONSTRAINT` в
> более поздних миграциях для тех же столбцов. Примененные миграции
> (`0001..0009`) — **не редактируются** (см. правило ниже).

---

## Правило 1 — constraints inline с `CREATE TABLE`

Для **каждой новой таблицы** все DB-level инварианты, относящиеся к ее
собственным колонкам, добавляются в **том же** файле миграции, где
`CREATE TABLE`:

- `PRIMARY KEY` / `UNIQUE` / partial `UNIQUE … WHERE …`;
- `FOREIGN KEY` на таблицы в той же БД (с явным `ON DELETE
  RESTRICT|CASCADE|SET NULL` — без default);
- `CHECK` для каждого ограничения domain-уровня (regex имени, длина
  description, cardinality labels, enum status, диапазоны чисел и т.д.) —
  зеркало domain newtypes;
- `EXCLUDE USING gist (…)` для не-пересекающихся диапазонов / CIDR /
  временных интервалов;
- индексы, обслуживающие request-path запросы и references (`(project_id,
  created_at, id)`, `(parent_id)`, и т.п.);
- триггеры, если они часть инварианта (например, outbox-NOTIFY на этом
  ресурсе).

Пример каркаса миграции для нового ресурса:

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE my_resource (
    id          text PRIMARY KEY,
    project_id   text NOT NULL,                                -- cross-service: без FK
    name        text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    labels      jsonb NOT NULL DEFAULT '{}'::jsonb,
    parent_id   text NOT NULL REFERENCES parents(id) ON DELETE RESTRICT,
    status      text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT my_resource_name_check
        CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$'),
    CONSTRAINT my_resource_description_check
        CHECK (length(description) <= 256),
    CONSTRAINT my_resource_labels_check
        CHECK (kacho_labels_valid(labels)),
    CONSTRAINT my_resource_status_check
        CHECK (status IN ('PROVISIONING', 'ACTIVE', 'DELETING', 'FAILED'))
);

CREATE UNIQUE INDEX my_resource_project_id_name_key
    ON my_resource (project_id, name)
 WHERE name <> '';

CREATE INDEX my_resource_parent_id_idx ON my_resource (parent_id);

-- +goose StatementEnd
```

**Запрещено** добавлять CHECK / FK / UNIQUE / EXCLUDE этой же таблицы в
последующих миграциях (`0NNN+1_*.sql` ALTER-ом) — кроме случаев когда
ограничение **физически появилось позже** (новая колонка, новая семантика,
новая parent-таблица).

---

## Правило 2 — parity с `domain.Validate`

Каждое ограничение, выраженное в Go-domain (`internal/domain/types.go` +
`kacho-corelib/validate`), **обязано** иметь DB-level CHECK поверх:

| Domain rule                              | DB CHECK                                                                        |
| ---------------------------------------- | ------------------------------------------------------------------------------- |
| `NameVPC` (permissive regex)             | `CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$')`               |
| `NameGateway` (strict — на service-слое)  | `CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$')` (permissive DB-regex; строгая проверка lowercase — в service-слое) |
| `Description ≤ 256`                      | `CHECK (length(description) <= 256)`                                            |
| `Labels` (≤64 пар + key regex + length)  | `CHECK (kacho_labels_valid(labels))` (общая функция в схеме `kacho_vpc`)        |
| `Status` (enum-set)                      | `CHECK (status IN ('PROVISIONING', 'ACTIVE', ...))`                             |
| Семейный cardinality (`≤ 1 v4`, `≤ 1 v6`)| `CHECK (jsonb_array_length(v4_address_ids) <= 1)` и симметрично v6              |
| Bounded числовое поле                    | `CHECK (selector_priority >= 0)`                                                |

`domain.Validate` — это fast-fail для пользователей, **но БД — последний рубеж**
от внешних writers (admin SQL-консоль), миграций, аварийных восстановлений и багов
в app-коде, которые пропустят `Validate`.

Service-уровень обязан мапить SQLSTATE → gRPC code:
`23503 → FailedPrecondition`, `23505 → AlreadyExists / FailedPrecondition`
(по контексту), `23514 → InvalidArgument`, `23P01 → FailedPrecondition`.

---

## Правило 3 — within-service refs vs cross-service refs

**Within-service** (ссылка на ресурс в той же БД `kacho_vpc` — Network,
Subnet, Address, RouteTable, SecurityGroup, Gateway,
NetworkInterface, AddressPool и связные таблицы):

- **Обязателен FK** `REFERENCES <table>(id)` с **explicit** `ON DELETE
  {RESTRICT | CASCADE | SET NULL}`. Default (`NO ACTION`) — запрещен, политика
  каскада должна быть явной.
- Software-side `Get → check → Update` (TOCTOU) **запрещен** (race-prone).
  Атомарность — `UNIQUE` / partial `UNIQUE WHERE …` / `EXCLUDE` / условный
  `UPDATE … WHERE <invariant> RETURNING …` (CAS).

**Cross-service** (ссылка на ресурс в чужой БД — `project_id` из
`kacho-iam`, `zone_id` из `kacho-geo`, `instance_id` из
`kacho-compute`, и т.п.):

- **FK запрещен** — database-per-service не допускает cross-DB ссылок на уровне
  БД. Колонка хранится как `text NOT NULL` без FK.
- Валидация существования — на request-path service-слоя через типизированный
  gRPC-клиент к peer-сервису-владельцу.

---

## Правило 4 — UNIQUE для имен

Все 7 пользовательских ресурсов VPC (Network, Subnet, Address, RouteTable,
SecurityGroup, Gateway, NetworkInterface) — project-level, и
для каждого действует **`(project_id, name)` UNIQUE** в пределах project.

Семантика:

- **Network** (исторический baseline) — non-partial `UNIQUE (project_id, name)`
  (`networks_project_id_name_key`), name никогда не пустое.
- Остальные ресурсы — **partial** `UNIQUE (project_id, name) WHERE name <> ''`
  (имя опционально; пустые имена дубликатов не образуют). Задается inline в
  `0001_initial.sql` — реализует контракт Kachō `ALREADY_EXISTS` на дубль имени.

Для **нового** ресурса этого сервиса — partial-UNIQUE добавляется в том же
файле, что `CREATE TABLE` (правило 1).

Помимо `(project_id, name)`, partial UNIQUE применяется к естественным
инвариантам одного значения (один IP — один Address):

```sql
CREATE UNIQUE INDEX addresses_external_pool_ip_uniq
    ON addresses ((external_ipv4 ->> 'address_pool_id'),
                  (external_ipv4 ->> 'address'))
 WHERE (external_ipv4 ->> 'address') <> '';
```

Партиал-UNIQUE на «owner-колонку» (для one-resource-per-owner-or-many)
**не** используется — корректный инвариант там — атомарный CAS (см. правило 3).

---

## Правило 5 — timestamps

Каждая таблица обязана иметь:

```sql
created_at timestamptz NOT NULL DEFAULT now()
```

`updated_at` / `modified_at` — добавляется только если по бизнес-логике это
часть API (например, `address_pools.modified_at`). Дополнительные `_at`
поля — `timestamptz` без `time zone` short-form, явный `NOT NULL DEFAULT
now()` (или nullable + `NULL` default — но тогда комментарий «почему
nullable»).

Proto-ответ truncate'ит время до секунд — БД хранит микросекунды без изменений.

---

## Правило 6 — формат `id`

Идентификаторы — `text PRIMARY KEY`, формат «3-char crockford-base32 prefix
+ 17-char crockford-base32 random» (см. `kacho-corelib/ids`).

Допустимы только `text` колонки для id — `uuid` не использовать
(api-gateway маршрутизирует по prefix-у первых 3 символов; uuid-формат
несовместим).

---

## Правило 7 — outbox + LISTEN/NOTIFY

Новый пользовательский ресурс обязан эмитить события `CREATED` / `UPDATED` /
`DELETED` в общую таблицу `vpc_outbox` (присутствует в `0001_initial.sql`).
Триггер `vpc_outbox_notify_trg` шлет `pg_notify('vpc_outbox',
sequence_no::text)`.

Что нужно от **миграции** нового ресурса:

- ничего дополнительно: outbox-таблица и trigger-функция `vpc_outbox_notify()`
  уже существуют; миграция нового ресурса **не** трогает `vpc_outbox`.

Что нужно от **сервис-кода**:

- в той же транзакции, что и мутация ресурса, делать `INSERT INTO vpc_outbox
  (event_type, resource_kind, resource_id, data) VALUES (...)`. Без этого
  событие не попадет в транзакционный outbox-журнал (in-cluster `LISTEN/NOTIFY`;
  публичного Watch RPC нет — клиенты наблюдают через polling `List` / `OperationService.Get`).

Если будущий ресурс требует **другой** outbox-таблицы (например,
data-plane-ивенты на `network_interfaces_dataplane_outbox`) — она создается в
той же миграции что и сам ресурс (правило 1).

---

## Цепочка миграций: `0001` baseline + `0002..0009` инкрементальные

`0001_initial.sql` — **squashed baseline** (greenfield): весь текущий state схемы
свернут в одну стартовую миграцию. Далее идут **инкрементальные** миграции
`0002..0009`, каждая со своей семантикой:

- `0001_initial.sql` — все таблицы создаются сразу в схеме `kacho_vpc`; все CHECK /
  FK / UNIQUE / EXCLUDE / generated columns / триггеры — **inline с `CREATE TABLE`**
  (правило 1), в т.ч. partial `UNIQUE (project_id, name)` для имен.
- `0002` / `0003` — `DROP`-миграции (снятие отживших колонок/полей).
- `0004` — `address_pool_cidrs` + EXCLUDE gist (CIDR no-overlap per kind).
- `0005` / `0007` / `0008` / `0009` — `ALTER` / `ADD`: новые колонки, FK, индексы и
  семантика, физически появившиеся позже baseline (`default_security_group_id` + FK
  ON DELETE SET NULL + partial UNIQUE one-default-SG-per-network; outbox-колонки для
  регистрации owner-tuple; доп. колонки на таблицах и т.п.).
- `0006` — `fga_register_outbox` (transactional-outbox для регистрации owner-tuple).

Helper-функции включены в baseline (под `kacho_vpc` schema):
`kacho_labels_valid`, `vpc_outbox_notify`, `rt_auto_assoc_subnets`,
`subnet_auto_pick_rt`, `subnets_outbox_emit_route_table_change`.

**Запрещено**: редактирование **примененной** миграции — ни `0001_initial.sql`, ни
любой из `0002..0009`. Любая корректировка схемы — **новая миграция**
`0NNN_<topic>.sql` с инкрементным номером (следующий — `0010_*`).

---

## Чек-лист для нового файла миграции

При добавлении `internal/migrations/0NNN_<topic>.sql`:

- [ ] Файл назван `0NNN_<snake_case_topic>.sql` (инкрементный номер, без
      пропусков).
- [ ] Содержит `-- +goose Up` / `-- +goose Down` секции; multi-statement DDL
      обернут в `-- +goose StatementBegin` / `-- +goose StatementEnd`.
- [ ] Если создается таблица — **все** ее CHECK / FK / UNIQUE / EXCLUDE /
      индексы в **этом же** файле (правило 1).
- [ ] FK явный `ON DELETE RESTRICT|CASCADE|SET NULL` — без default
      `NO ACTION` (правило 3).
- [ ] Для каждого domain-ограничения (regex, length, enum) — соответствующий
      DB CHECK (правило 2).
- [ ] `(project_id, name)` UNIQUE (partial-если-name-опционально) для
      пользовательского ресурса (правило 4).
- [ ] `created_at timestamptz NOT NULL DEFAULT now()` (правило 5).
- [ ] `id text PRIMARY KEY` (правило 6).
- [ ] Если ALTER на applied-таблицу — обоснование в шапке («новая колонка
      X», «новая семантика Y») + при необходимости diagnostic `DO $$ …
      RAISE EXCEPTION P0001 …` pre-check + `ADD CONSTRAINT … NOT VALID` +
      `VALIDATE CONSTRAINT`.
- [ ] Integration-тест в `internal/repo/*integration_test.go` падает с
      ожидаемым SQLSTATE на нарушении нового CHECK / FK / UNIQUE (тесты в том
      же PR).
- [ ] Newman-кейс (если новый RPC) — black-box проверка ошибки через
      api-gateway (тесты в том же PR).
</content>
</invoke>
