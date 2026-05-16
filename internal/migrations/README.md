# `kacho-vpc` migrations — правила написания

Этот документ — нормативный регламент для **новых** миграций в `internal/migrations/`.
Закрывает правило **E.5** skill [`evgeniy`](../../../../../kacho-workspace/.claude/skills/evgeniy/SKILL.md)
(«constraint / index / FK — близко к declaration таблицы, в той же миграции, не в
следующей»). Все правила ниже — **обязательные**.

Источники истины (читать вместе с этим файлом):

- workspace-`CLAUDE.md` → раздел «Within-service refs — DB-уровень обязателен» и
  §«Запреты» #5/#10.
- `kacho-vpc/CLAUDE.md` §12 «Migrations» — где живут миграции, какая `MigrateDSN`,
  как embedded через `embed.FS`.
- `kacho-vpc/CLAUDE.md` §«Schema = `kacho_vpc`» — search-path и schema-qualified DDL.
- skill `evgeniy` §5 (E.1–E.7) — БД как «последний рубеж» валидации.
- skill `evgeniy` §11 / §13 (KAC-99 Wave 2 — `NOT VALID` + `VALIDATE` workflow).

> **TL;DR:** новая таблица — все её CHECK / FK / UNIQUE / EXCLUDE / index в **том же**
> файле миграции, где `CREATE TABLE`. Никаких post-hoc `ALTER TABLE ADD CONSTRAINT` в
> более поздних миграциях для тех же столбцов. Применённые миграции
> (`0001..0034`) — **не редактируются** (запрет #5 workspace-CLAUDE.md).

---

## Правило 1 — E.5: constraints inline с `CREATE TABLE`

Для **каждой новой таблицы** все DB-level инварианты, относящиеся к её
собственным колонкам, добавляются в **том же** файле миграции, где
`CREATE TABLE`:

- `PRIMARY KEY` / `UNIQUE` / partial `UNIQUE … WHERE …`;
- `FOREIGN KEY` на таблицы в той же БД (с явным `ON DELETE
  RESTRICT|CASCADE|SET NULL` — без default);
- `CHECK` для каждого ограничения domain-уровня (regex имени, длина
  description, cardinality labels, enum status, диапазоны чисел и т.д.) —
  скрин-параллель с domain newtypes;
- `EXCLUDE USING gist (…)` для не-пересекающихся диапазонов / CIDR /
  временных интервалов;
- индексы, обслуживающие request-path запросы и references (`(folder_id,
  created_at, id)`, `(parent_id)`, и т.п.);
- триггеры, если они часть инварианта (например, outbox-NOTIFY на этом
  ресурсе).

Пример каркаса миграции для нового ресурса:

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE my_resource (
    id          text PRIMARY KEY,
    folder_id   text NOT NULL,                                -- cross-service: без FK
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

CREATE UNIQUE INDEX my_resource_folder_id_name_key
    ON my_resource (folder_id, name)
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
| `NameGateway` (strict, lowercase)        | `CHECK (name ~ '^([a-z]([-_a-z0-9]{0,61}[a-z0-9])?)?$')`                        |
| `Description ≤ 256`                      | `CHECK (length(description) <= 256)`                                            |
| `Labels` (≤64 пар + key regex + length)  | `CHECK (kacho_labels_valid(labels))` (общая функция в схеме `kacho_vpc`)        |
| `Status` (enum-set)                      | `CHECK (status IN ('PROVISIONING', 'ACTIVE', ...))`                             |
| Семейный cardinality (`≤ 1 v4`, `≤ 1 v6`)| `CHECK (jsonb_array_length(v4_address_ids) <= 1)` и симметрично v6              |
| Bounded числовое поле                    | `CHECK (selector_priority >= 0)`                                                |

Skill `evgeniy` §5 E.1: domain.Validate — это fast-fail для пользователей,
**но БД — последний рубеж** от внешних writers (admin SQL-консоль), миграций,
аварийных восстановлений и багов в app-коде, которые пропустят `Validate`.

Service-уровень обязан мапить SQLSTATE → gRPC code:
`23503 → FailedPrecondition`, `23505 → AlreadyExists / FailedPrecondition`
(по контексту), `23514 → InvalidArgument`, `23P01 → FailedPrecondition`
(см. workspace-CLAUDE.md «Within-service refs»).

---

## Правило 3 — within-service refs vs cross-service refs

**Within-service** (ссылка на ресурс в той же БД `kacho_vpc` — Network,
Subnet, Address, RouteTable, SecurityGroup, Gateway, PrivateEndpoint,
NetworkInterface, AddressPool и связные таблицы):

- **Обязателен FK** `REFERENCES <table>(id)` с **explicit** `ON DELETE
  {RESTRICT | CASCADE | SET NULL}`. Default (`NO ACTION`) — запрещён, политика
  каскада должна быть явной.
- Software-side `Get → check → Update` (TOCTOU) **запрещён** (workspace
  §запрет #10). Атомарность — `UNIQUE` / partial `UNIQUE WHERE …` /
  `EXCLUDE` / условный `UPDATE … WHERE <invariant> RETURNING …` (CAS).

**Cross-service** (ссылка на ресурс в чужой БД — `folder_id` из
`kacho-resource-manager`, `zone_id` из `kacho-compute`, `instance_id` из
`kacho-compute`, и т.п.):

- **FK запрещён** — workspace §запрет #8 (database-per-service запрещает
  cross-DB ссылки на уровне БД). Колонка хранится как `text NOT NULL` без
  FK.
- Валидация существования — на request-path service-слоя через типизированный
  gRPC-клиент к peer-сервису-владельцу. См. workspace-CLAUDE.md §«Кросс-доменные
  ссылки на ресурсы».

---

## Правило 4 — UNIQUE для имён

Все 8 пользовательских ресурсов VPC (Network, Subnet, Address, RouteTable,
SecurityGroup, Gateway, PrivateEndpoint, NetworkInterface) — folder-level, и
для каждого действует **`(folder_id, name)` UNIQUE** в пределах folder.

Семантика:

- **Network** (исторический baseline) — non-partial `UNIQUE (folder_id, name)`
  (`networks_folder_id_name_key`), name никогда не пустое.
- Остальные ресурсы — **partial** `UNIQUE (folder_id, name) WHERE name <> ''`
  (имя опционально; пустые имена дубликатов не образуют). Введено миграцией
  `0002_resource_name_unique.sql` для приведения к verbatim YC `409
  AlreadyExists` (см. `kacho-vpc/CLAUDE.md` §12).

Для **нового** ресурса этого сервиса — partial-UNIQUE добавляется в том же
файле, что `CREATE TABLE` (правило 1).

Помимо `(folder_id, name)`, partial UNIQUE применяется к естественным
инвариантам одного значения (один IP — один Address):

```sql
CREATE UNIQUE INDEX addresses_external_pool_ip_uniq
    ON addresses ((external_ipv4 ->> 'address_pool_id'),
                  (external_ipv4 ->> 'address'))
 WHERE (external_ipv4 ->> 'address') <> '';
```

Партиал-UNIQUE на «owner-колонку» (для one-resource-per-owner-or-many)
**не** используется — корректный инвариант там — атомарный CAS (см. правило
3 + workspace «Within-service refs»; история отката — миграции 0016 → 0017).

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

Proto-ответ truncate'ит время до секунд (`kacho-vpc/CLAUDE.md` §11) — БД
хранит микросекунды без изменений.

---

## Правило 6 — формат `id`

Идентификаторы — `text PRIMARY KEY`, формат «3-char crockford-base32 prefix
+ 17-char crockford-base32 random» (см. `kacho-vpc/CLAUDE.md` §3 / `kacho-corelib/ids`).

Допустимы только `text` колонки для id — `uuid` не использовать
(api-gateway маршрутизирует по prefix-у первых 3 символов; uuid-формат
несовместим).

---

## Правило 7 — outbox + LISTEN/NOTIFY

Новый пользовательский ресурс обязан эмитить события `CREATED` / `UPDATED` /
`DELETED` в общую таблицу `vpc_outbox` (присутствует в `0001_initial.sql`).
Триггер `vpc_outbox_notify_trg` шлёт `pg_notify('vpc_outbox',
sequence_no::text)`.

Что нужно от **миграции** нового ресурса:

- ничего дополнительно: outbox-таблица и trigger-функция `vpc_outbox_notify()`
  уже существуют; миграция нового ресурса **не** трогает `vpc_outbox`.

Что нужно от **сервис-кода**:

- в той же транзакции, что и мутация ресурса, делать `INSERT INTO vpc_outbox
  (event_type, resource_kind, resource_id, data) VALUES (...)`. Без этого
  `InternalWatchService` не отдаст событие наружу.

Если будущий ресурс требует **другой** outbox-таблицы (например,
data-plane-ивенты на `network_interfaces_dataplane_outbox`) — она создаётся в
той же миграции что и сам ресурс (правило 1).

---

## Baseline-исключение: миграции `0001..0034` — applied, не редактируются

Текущий state БД сформирован миграциями `0001..0034`. Из них:

- `0001_initial.sql` — squashed baseline 2026-05-11 (22 исторических
  миграции свернуты, см. `kacho-vpc/CLAUDE.md` §12). Все базовые таблицы
  + FK + EXCLUDE + UNIQUE + generated columns там inline (правило 1
  соблюдено для baseline).
- `0002_resource_name_unique.sql` — partial UNIQUE для 6 ресурсов
  (приведено к verbatim YC; non-partial baseline был только у Network).
- `0003..0024` — последовательные доработки схемы (новые колонки, FK,
  trigger-функции auto-association, и т.п.). Каждая включает свои
  constraints inline (правило 1).
- `0025..0033` — **ретроспективное** добавление CHECK constraints
  (`name` regex, `description ≤ 256`, `labels` cardinality+key-regex,
  status enum, cardinality v4/v6) для уже **существующих** таблиц после
  введения domain newtypes. Это **ретроспективное закрытие правила 2**
  (parity с domain.Validate) — допустимое **исключение** из правила 1,
  потому что таблицы уже были созданы более ранними миграциями. Файлы
  `0025..0033` — пилот скилла `evgeniy` Wave 2 / KAC-99 (`NOT VALID` +
  `VALIDATE` workflow с диагностическим `DO $$ … RAISE EXCEPTION P0001`
  pre-check). Для **нового** кода правило 1 действует в полной форме:
  CHECK обязан появиться в той же миграции, что `CREATE TABLE`.
- `0034_schema_rename_to_kacho_vpc.sql` — перенос всех таблиц / функций /
  `goose_db_version` в схему `kacho_vpc` (skill `evgeniy` §5 E.4).

**Запрещено** (workspace-CLAUDE.md §запрет #5): редактирование любого из
файлов `0001..0034`. Любая корректировка — только новая миграция.

---

## Чек-лист для нового файла миграции

При добавлении `internal/migrations/0NNN_<topic>.sql`:

- [ ] Файл назван `0NNN_<snake_case_topic>.sql` (инкрементный номер, без
      пропусков).
- [ ] Содержит `-- +goose Up` / `-- +goose Down` секции; multi-statement DDL
      обёрнут в `-- +goose StatementBegin` / `-- +goose StatementEnd`.
- [ ] Если создаётся таблица — **все** её CHECK / FK / UNIQUE / EXCLUDE /
      индексы в **этом же** файле (правило 1).
- [ ] FK явный `ON DELETE RESTRICT|CASCADE|SET NULL` — без default
      `NO ACTION` (правило 3).
- [ ] Для каждого domain-ограничения (regex, length, enum) — соответствующий
      DB CHECK (правило 2).
- [ ] `(folder_id, name)` UNIQUE (partial-если-name-опционально) для
      пользовательского ресурса (правило 4).
- [ ] `created_at timestamptz NOT NULL DEFAULT now()` (правило 5).
- [ ] `id text PRIMARY KEY` (правило 6).
- [ ] Если ALTER на applied-таблицу — обоснование в шапке («новая колонка
      X», «новая семантика Y») + при необходимости diagnostic `DO $$ …
      RAISE EXCEPTION P0001 …` pre-check + `ADD CONSTRAINT … NOT VALID` +
      `VALIDATE CONSTRAINT` (паттерн KAC-99 / Wave 2).
- [ ] Integration-тест в `internal/repo/*integration_test.go` падает с
      ожидаемым SQLSTATE на нарушении нового CHECK / FK / UNIQUE (workspace
      §запрет #11 — тесты в том же PR).
- [ ] Newman-кейс (если новый RPC) — black-box проверка ошибки через
      api-gateway (workspace §запрет #11).
