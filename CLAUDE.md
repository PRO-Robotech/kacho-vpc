# kacho-vpc — CLAUDE.md

VPC-специфичный CLAUDE.md, дополняющий общий `kacho-workspace/CLAUDE.md`. Этот
файл — обязательный контекст при работе из `kacho-vpc/` и любых его подпапок.

## 1. Что это за сервис

Sub-phase 0.3 продукта Kachō. gRPC-сервис управления сетевой инфраструктурой:
**Network, Subnet, Address, RouteTable, SecurityGroup, Gateway, PrivateEndpoint**.
Цель — verbatim parity с Yandex Cloud VPC API (proto-форма, error texts,
status codes, timestamp precision, regex'ы, behavioural semantics).

В скоупе:
- 7 ресурсов с CRUD + Move + ListOperations.
- Subnet: AddCidrBlocks / RemoveCidrBlocks / Relocate / ListUsedAddresses.
- Address: GetByValue / ListBySubnet (для ref-validation в peer-сервисах).
- SecurityGroup: UpdateRules / UpdateRule + auto-default-on-network.
- Internal endpoints (на отдельном порту 9091, не маршрутизируется наружу):
  `InternalWatchService` (outbox stream через LISTEN/NOTIFY),
  `InternalAddressService` (allocate/free internal IP для kacho-vpc-controllers).
- Outbox + LISTEN/NOTIFY для event stream.

Вне скоупа:
- Реальный data plane (это control plane only, как и весь Kachō).
- Default SG creation — выполняется reconciler-loop'ом в `kacho-vpc-controllers`,
  не самим VPC-сервисом (см. POST-PROCESSING-IN-CONTROLLERS).
- DNS-records для Address — proto-поля есть (`dns_record_specs`), но сервис их
  пока игнорирует (TODO в `TODO.md`).

## 2. Доменная модель и связи

```
Network (1) ──┬──→ (N) Subnet ──→ (N) Address (internal)
              ├──→ (N) RouteTable
              ├──→ (N) SecurityGroup ──→ (N) SecurityGroupRule
              └─── default_security_group_id (FK на одну из SG этой сети)

Address (external)  — folder-level, без Subnet
Gateway             — folder-level, не привязан к Network (shared_egress)
PrivateEndpoint     — привязан к Network + Subnet (privatelink)
```

Все ресурсы — folder-level (`folder_id` обязателен в Create). Все таблицы
**flat** (без K8s envelope `resource_version`/`generation`/`deletion_timestamp`/
`finalizers`/`spec`/`status`) — это были artefacts от envelope-эпохи до 1.0
rewrite, в текущих `internal/migrations/0002–0012` их нет. `cloud_id` /
`organization_id` в схеме отсутствуют — фильтрация только по `folder_id`.

**FK contract:**
- Network → Subnet, RouteTable, SG: RESTRICT (нельзя удалить Network с детьми)
- Subnet → Address (internal): RESTRICT
- Network → default_security_group_id: ON DELETE SET NULL (default SG удаляется
  явно перед Network)

## 3. Resource ID format

Все ресурсы получают ID через `kacho-corelib/ids.NewID(<prefix>)`:

| Ресурс           | Prefix                  | Пример                       |
|------------------|-------------------------|------------------------------|
| Network          | `ids.PrefixNetwork`     | `net + 17-char base32`       |
| Subnet           | `ids.PrefixSubnet`      | `sub + ...`                  |
| Address          | `ids.PrefixAddress`     | `adr + ...`                  |
| RouteTable       | `ids.PrefixRouteTable`  | `rtb + ...`                  |
| SecurityGroup    | `ids.PrefixSecurityGroup` | `sgp + ...`                |
| Gateway          | `ids.PrefixGateway`     | `gtw + ...`                  |
| PrivateEndpoint  | `ids.PrefixPrivateEndpoint` | `pep + ...`              |
| Operation        | `ids.PrefixOperationVPC` | `opvpc + ...`               |

Колонки `id` в миграциях — `TEXT` (после миграции 0009 `id_format_to_text`).
**Не использовать UUID-валидацию на входе** — verbatim YC: garbage-id даёт
async `NotFound`, а не sync `InvalidArgument` (см. `ac61127`).

## 4. Архитектурные паттерны (VPC-специфичные)

### 4.1 Operations (Long Running Operations)

Все мутации (`Create/Update/Delete/Move/AddCidrBlocks/...`) возвращают
`*operation.Operation`, реальная работа — в worker-горутине через
`operations.Run(ctx, opsRepo, opID, fn)`.

Шаблон:
```go
func (s *Service) Create(ctx context.Context, req CreateReq) (*operations.Operation, error) {
    // 1. SYNC: валидация + sanitization
    if err := corevalidate.NameVPC("name", req.Name); err != nil { return nil, err }
    // ... другие sync-проверки ...

    // 2. Создать Operation
    resID := ids.NewID(ids.PrefixXxx)
    op, err := operations.New(
        ids.PrefixOperationVPC,
        fmt.Sprintf("Create xxx %s", req.Name),
        &vpcv1.CreateXxxMetadata{XxxId: resID},
    )
    if err != nil { return nil, err }
    if err := s.opsRepo.Create(ctx, op); err != nil { return nil, err }

    // 3. ASYNC: Worker
    operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
        return s.doCreate(ctx, resID, req)
    })

    return &op, nil
}
```

Внутри `doCreate`:
- folder existence check через `folderClient.Exists` → `NotFound` если не найден
  (verbatim YC text: `"Folder with id <X> not found"`)
- network existence (для Subnet/SG/RT/PE) → `NotFound` (`"Network %s not found"`)
- repo.Insert → возвращает domain-модель
- `return anypb.New(domainXxxToProto(created))` — успех
- worker возвращает `nil, error` — операция помечается failed с `google.rpc.Status`

### 4.2 Operation Delete-response = Empty

Согласно proto-options всех Delete RPC: `metadata: "DeleteXxxMetadata", response:
"google.protobuf.Empty"`. Worker должен возвращать `anypb.New(&emptypb.Empty{})`,
metadata уже в `Operation.metadata` (передана при `operations.New`).

⚠️ Текущий код кладёт `DeleteXxxMetadata` в response для всех 6 Delete-операций —
это нарушение контракта (см. `TODO.md` #1).

### 4.3 Outbox + LISTEN/NOTIFY

Каждая успешная мутация в `service/*.go` (через worker) пишет событие в
`vpc_outbox` в той же транзакции, что и сам ресурс. Триггер
`vpc_outbox_notify_trg` шлёт `pg_notify('vpc_outbox', sequence_no::text)`.

`InternalWatchService.Watch(req)`:
1. Acquire dedicated `pgx.Conn` (LISTEN не работает на pooled conn).
2. `LISTEN vpc_outbox`.
3. Catchup: `SELECT * FROM vpc_outbox WHERE sequence_no > req.from_sequence_no`.
4. Loop: `conn.WaitForNotification(ctx)` → читать новые события → стрим клиенту.
5. `defer UNLISTEN vpc_outbox + conn.Release()`.

Текущая реализация использует `pool.Acquire(ctx)` + `Release()` — conn
возвращается в пул с `UNLISTEN` defer, но при abnormal exit может остаться
"грязным". Рекомендуется использовать `pgx.Connect(...)` вне пула для
полной изоляции.

### 4.4 UpdateMask discipline

YC verbatim: каждое Update RPC принимает `google.protobuf.FieldMask`.

**Decision table:**
- mask содержит unknown поле → `InvalidArgument` (через `corevalidate.UpdateMask` с known-set).
- mask содержит **immutable** поле → `InvalidArgument` (`"<field> is immutable after Xxx.Create"`).
- mask пустой → full-object PATCH: применяются все mutable поля; immutable из
  тела запроса **silently игнорируются** (это verbatim YC behaviour, см. `8158a84`).
- mask содержит mutable поле → применяется; валидируется по тем же правилам, что Create.

**Immutable fields:**
- Subnet: `v4_cidr_blocks`, `v6_cidr_blocks`, `network_id`, `zone_id`
- Address: `external_ipv4_address_spec`, `internal_ipv4_address_spec`, `folder_id`
- остальные ресурсы: `folder_id` (управляется через Move)

Шаблон проверки в начале Update:
```go
for _, field := range req.UpdateMask {
    switch field {
    case "v4_cidr_blocks", "v6_cidr_blocks", "network_id", "zone_id":
        return nil, invalidArg(field, field+" is immutable after Subnet.Create")
    }
}
```

### 4.5 Filter parsing

`List*` RPC принимают `filter` строку YC-syntax: `name="<value>"`. Парсится
через `kacho-corelib/filter.Parse` с whitelist полей. Поддерживается только
`name=` для текущей фазы (см. `2f340d6`).

### 4.6 Pagination — cursor-based

`(created_at, id)` ORDER BY ASC, ASC. `page_token` — opaque base64 структуры
`{created_at, id}`. `page_size` валидируется через `corevalidate.PageSize`
(0 → DefaultPageSize=50, max 1000). Garbage page_token → `InvalidArgument`
(см. `5d16961`, `8de9366`).

## 5. Validation layering

**Sync (до создания Operation):**
- Required-поля: `folder_id`, `network_id`, `name` (где обязательно), `zone_id`
- Format: `corevalidate.NameVPC` (permissive: empty + uppercase + underscore),
  `Description` (≤256), `Labels` (≤64 пар, key regex), `ZoneId` (whitelist
  `ru-central1-{a,b,c,d}`)
- CIDR: `validateCIDRPrefix` (host-bits = 0)
- DhcpOptions: `domain_name` RFC 1123, `domain_name_servers[]` / `ntp_servers[]` IP
- UpdateMask: known-set + immutable check
- DeletionProtection: проверяется sync перед Delete (`333c535`)
- Address spec: oneof external/internal — exactly one

**Async (внутри Operation worker):**
- folder existence через `folderClient.Exists` → `NotFound`
- network existence (для дочерних ресурсов) → `NotFound`
- Repo Insert/Update — FK violations, EXCLUDE constraint (CIDR overlap),
  UNIQUE violation (name within folder)
- Все эти ошибки маппятся через `mapRepoErr` в gRPC-status

## 6. Error mapping

`internal/service/network.go::mapRepoErr` — единая точка трансляции:

| Sentinel error           | gRPC code             | Verbatim YC text source           |
|--------------------------|-----------------------|------------------------------------|
| `ErrNotFound`            | `NOT_FOUND`           | `"<Resource> %s not found"`       |
| `ErrAlreadyExists`       | `ALREADY_EXISTS`      | `"<resource> with name ... exists"` |
| `ErrFailedPrecondition`  | `FAILED_PRECONDITION` | varies (e.g. `"network is not empty"`) |
| `ErrInvalidArg`          | `INVALID_ARGUMENT`    | varies (CIDR overlap, etc.)       |
| `ErrInternal`            | `INTERNAL`            | `"internal database error"` (no leak) |

`stripSentinel` удаляет префикс sentinel из текста, чтобы клиент видел verbatim
YC сообщение без internal-обёртки.

**Specific mappings:**
- CIDR overlap (PG SQLSTATE `23P01` от EXCLUDE constraint) → `FAILED_PRECONDITION`
  с текстом `"Subnet CIDRs can not overlap"` (см. `e015191`, `e43996b`).
- garbage UUID format в id → **NE syncing** в `InvalidArgument`. Async через
  `repo.Get` → `NotFound` (verbatim YC, `ac61127`).
- duplicate name → `ALREADY_EXISTS` (UNIQUE constraint violation `23505`).

## 7. Hard-delete (не soft-delete)

С фазы 1.0 — `DELETE FROM <table> WHERE id = $1`. Никаких `deletion_timestamp`
для tombstones (поле в схеме осталось от envelope-эпохи, не используется в
business-logic). См. `4e3e7ec`.

## 8. CIDR semantics

### 8.1 Layered overlap detection

1. **Sync-проверка** (для AddCidrBlocks): `checkCIDRDisjoint` — CIDR'ы внутри
   одного запроса не должны пересекаться между собой.
2. **DB-level** (миграция 0007): `EXCLUDE USING gist` constraint
   `subnets_no_overlap_v4` / `subnets_no_overlap_v6`. Atomic, race-free
   защита от пересечения с соседними Subnet в той же Network. SQLSTATE `23P01`
   маппится на `FailedPrecondition`.

⚠️ **Известное ограничение**: EXCLUDE проверяет только `v4_cidr_primary` (array[1]).
При `AddCidrBlocks` второй+ CIDR не проверяется на DB-уровне; покрывается
сервис-level через `networkRepo.List` (см. `subnet.go:382-388`).

### 8.2 Host-bits

`10.0.0.5/24` отвергается с `INVALID_ARGUMENT` (`netip.Prefix.Masked() != prefix`).
Использовать `validateCIDRPrefix` в `service/validate.go`.

### 8.3 Internal IP в CIDR

Address.Create с explicit `internal_ipv4_address_spec.address` — sync проверка,
что IP попадает в один из `Subnet.v4_cidr_blocks`. Если нет → `INVALID_ARGUMENT`
(см. `254f4d5`).

### 8.4 Subnet.Relocate constraint

Если у Subnet есть Address-ресурсы (через `repo.AddressesBySubnet`) — Relocate
отвергается с `FailedPrecondition "Invalid subnet state"` (verbatim YC).

## 9. Default Security Group

При Network.Create:
1. SYNC создаётся Operation, возвращается клиенту.
2. ASYNC в worker: `repo.Insert(network)` → возвращается клиенту через
   Operation.response.
3. **Default SG не создаётся в этом сервисе** — это делает
   `kacho-vpc-controllers` через reconciler-loop, наблюдающий outbox.
   `Network.default_security_group_id` обновляется ассинхронно после.

При Network.Delete: worker должен сначала удалить default SG (если есть), потом
Network. Не-default SG препятствуют удалению (FK RESTRICT) → клиент получает
`FailedPrecondition "network is not empty"`.

## 10. Folder existence check

`folderClient` — gRPC-клиент к `kacho-resource-manager.FolderService.Exists`.
Используется в worker'е каждого Create/Move:
- `Exists(ctx, folderID)` → `(bool, error)`
- error → `Unavailable "folder check: <err>"`
- `false` → `NotFound "Folder with id <X> not found"`

Retry on `Unavailable` встроен в `kacho-corelib/retry` (см. `3569955`).

## 11. Timestamp precision

Все `created_at` truncate до **seconds** в proto-ответе (verbatim YC):
```go
CreatedAt: timestamppb.New(s.CreatedAt.Truncate(time.Second))
```
БД хранит микросекунды, но клиент видит только секунды (см. `ac61127`,
`YC-DIFF-TIMESTAMP-PRECISION`).

## 12. Migrations

**Боевые миграции** — в `internal/migrations/*.sql`, embedded через
`embed.FS`. Текущий список (12 файлов):
- `0001_operations.sql` (sync с corelib)
- `0002_networks.sql`
- `0003_subnets.sql`
- `0004_addresses.sql`
- `0005_route_tables.sql`
- `0006_addresses_subnet_fk.sql`
- `0007_subnets_cidr_exclude.sql` ← EXCLUDE constraint
- `0008_security_groups.sql`
- `0009_id_format_to_text.sql` ← UUID→TEXT
- `0010_vpc_outbox.sql`
- `0011_gateways.sql`
- `0012_private_endpoints.sql`

`migrations/` (в корне репо) — staging для `make sync-migrations` (только
`0001_operations.sql` от corelib). Source of truth — `internal/migrations/`.

**Запреты:**
- НЕ редактировать применённые миграции (запрет #5 из workspace CLAUDE.md).
- НЕ модифицировать `0001_operations.sql` локально — это копия из corelib.
- Новая миграция = новый файл с инкрементным номером.

**Schema flat — без K8s envelope:**

Все VPC-таблицы (Network, Subnet, Address, RouteTable, SecurityGroup, Gateway,
PrivateEndpoint) — flat: только domain-specific колонки + `id`/`folder_id`/
`name`/`description`/`labels`/`created_at`. **НЕТ** `resource_version`,
`generation`, `deletion_timestamp`, `finalizers`, `spec`, `status` (как JSONB).

Это было выкинуто в 1.0 rewrite (`fd372f7 !feat(1.0): rewrite to flat resources`).
Не вводить эти колонки в новых миграциях — они не нужны для flat API.

**Optimistic concurrency** для read-modify-write (UpdateRules-style) делается
через Postgres system column `xmin::text` (txid версия row), а не через
дополнительную колонку — это zero-overhead и не требует миграции:
```sql
SELECT field, xmin::text FROM t WHERE id = $1;
UPDATE t SET field = $2 WHERE id = $1 AND xmin::text = $3 RETURNING ...;
```

## 13. Local dev

```bash
# Поднять стенд (kind + helm + Postgres)
cd ../kacho-deploy && make dev-up

# Перезапустить только VPC
cd ../kacho-deploy && make reload-svc SVC=vpc

# Логи
cd ../kacho-deploy && make logs-svc SVC=vpc

# psql kacho_vpc
cd ../kacho-deploy && make psql SVC=vpc

# Миграции вручную (вне kind)
KACHO_VPC_DB_PASSWORD=secret bin/kacho-vpc migrate up

# Запуск unit-тестов
make test-short

# Запуск unit + integration (testcontainers)
make test

# Newman regression
cd newman && ./run-local.sh   # или см. newman/README.md
```

## 14. Тесты

Три уровня тестирования; за каждый отвечает отдельная инфраструктура.

### 14.1 Unit (`internal/service/*_test.go`, `internal/handler/*_test.go`)

Моки port-интерфейсов через ручные `mock_test.go`. Worker-горутины
`operations.Run` ждутся `time.Sleep(100ms)` — это TODO #10 (заменить на
`assert.Eventually`). Запуск: `make test-short`.

Если service-тест требует Postgres → это сигнал об утечке adapter в
use-case. См. `go-style-reviewer §3.11`.

### 14.2 Integration (`internal/repo/integration_test.go`)

Testcontainers с Postgres 16. Прогоняется только локально (`make test`),
CI пропускает через `-short` (TODO #17). Покрывает:
- Repo CRUD против реальной БД.
- EXCLUDE constraint поведение (CIDR overlap → 23P01).
- FK violations (Network с детьми → 23503).
- UNIQUE violations (duplicate name → 23505).
- Outbox emit транзакционность.

### 14.3 E2E / Postman (`newman/`)

**Главная regression-инфраструктура**. Отдельный домен, изолированный от
Go-кода (только HTTP вызовы через api-gateway). Структура:

```
newman/
├─ collections/
│  ├─ kacho-vpc.postman_collection.json       — master (single source of truth)
│  ├─ kacho-vpc-ro.postman_collection.json    — auto-generated read-only smoke
│  ├─ kacho-vpc-light.postman_collection.json — auto-generated light mutations
│  ├─ kacho-vpc-seq.postman_collection.json   — auto-generated heavy/sequential
│  ├─ kacho-vpc-internal.postman_collection.json — kacho-only (defaultSG, NetBox)
│  └─ kacho-vpc-pending.postman_collection.json — pending-parity holding pen
├─ environments/
│  ├─ local.postman_environment.json          — Kachō локальный (port-forward 18080)
│  └─ yc.postman_environment.json             — реальный YC API (IAM-токен)
├─ scripts/
│  ├─ run.sh                                  — quota-aware entrypoint
│  ├─ cleanup-vpc.sh                          — освобождает baseline в FOLDER/FOLDER_CROSS
│  └─ build-suite.py                          — генерирует ro/light/seq из master
├─ docs/TESTCASES.md                          — class taxonomy (CRUD/BVA/VAL/NEG)
└─ PARITY.md                                  — registry pending-parity / kacho-only
```

**Quota-aware 3-suite pipeline**: YC API имеет folder-level quota и
rate-limit ~2 POST/sec. Наивный прогон 100+ кейсов исчерпает квоту через
5-10 кейсов. Решение:

| Suite | ~Запросов | Delay | Назначение                                  |
|-------|-----------|-------|---------------------------------------------|
| `ro`  | ~30       | 50ms  | Read-only smoke (Get/List)                  |
| `light`| ~70      | 250ms | Light mutations (Create+Delete per case)    |
| `seq` | ~10       | 1500ms| Heavy/sequential (Move, multi-resource)     |

Pipeline (`./scripts/run.sh` без аргументов):
1. `cleanup-vpc.sh -y` (yc only) → освобождает baseline.
2. `build-suite.py` → пересобирает ro/light/seq из master.
3. Newman run `RO → LIGHT → SEQ` последовательно.
4. Reports в `out/last-run-{ro,light,seq}.json`.

**Контракт изоляции кейса** (00-preflight / 99-teardown):
- Каждая suite-collection начинается с `00-preflight`: создаёт Org/Cloud/Folder
  ad-hoc (local) или копирует `existingFolderId` (yc), сохраняет в
  `_suiteFolderId`/`_suiteOrgId`/`_suiteCloudId`.
- Каждая заканчивается `99-teardown`: cleanup только в local; в yc skip
  (managed quota).
- Каждый кейс работает только внутри `{{_suiteFolderId}}`, **не создаёт**
  свои Org/Cloud/Folder.

**Variable convention**:
- `{{baseUrl}}` — local: `http://localhost:18080`, yc: VPC API endpoint.
- `{{authToken}}` — IAM-токен (yc only, инжектится в run.sh из `yc iam create-token`).
- `{{runId}}` — random hex, уникализирует имена per-run.
- `{{_suiteFolderId}}` — главный folder для всех кейсов.
- `{{operationId}}` — для poll до `done=true`.

**Class taxonomy** (`docs/TESTCASES.md`): `CRUD-*` (happy path), `BVA-*`
(boundary value), `VAL-*` (field validation), `NEG-*` (negative). Полный
case-id: `<DOMAIN>-<ACTION>-<DETAIL>`, например `NET-CR-OK`,
`SUB-DEL-WITH-ADDR`, `ADR-CR-EXT-DDOS-ADV`.

**PARITY.md** — registry расхождений:
- `pending-parity`: кейс не в unified suite, потому что Kachō не соответствует
  YC; для исправления нужен PR в kacho-vpc.
- `kacho-only`: фичи которых нет в YC (defaultSG-auto, NetBox sync); живут
  в `kacho-vpc-internal.postman_collection.json`, только `--env local`.

**Запуск**:
```bash
# Локальный прогон (port-forward api-gateway → 18080 обязателен)
cd newman && ./scripts/run.sh --env local

# Против реального YC API (нужен yc CLI + IAM-токен)
./scripts/run.sh --env yc

# Только конкретный suite
./scripts/run.sh --suite ro
./scripts/run.sh --suite light
./scripts/run.sh --suite seq

# Один case (preflight + teardown auto-injected)
./scripts/run.sh --folder NET-CR-OK
./scripts/run.sh --env yc --folder NET-DEL-WITH-SUBNETS

# Пропустить cleanup перед прогоном (если quota уже свободна)
./scripts/run.sh --no-precleanup

# Пропустить rebuild ro/light/seq (если master не менялся)
./scripts/run.sh --no-rebuild
```

**Newman не в CI** (TODO #18) — целевая структура: `e2e-newman` job на
`docker compose up` локального стенда + `run.sh --env local --suite ro/light`.
Seq против YC — отдельный nightly job с секретным IAM-токеном.

Подробности добавления нового кейса, разрешённых паттернов и common pitfalls
см. в агенте `vpc-newman-author`.

## 15. Top-10 gotchas (из истории фиксов)

1. **Не валидировать UUID/id sync** — garbage id даёт async NotFound, не sync InvalidArgument (`ac61127`).
2. **NameVPC permissive, не strict** — empty/uppercase/underscore разрешены для
   Network/Subnet/Address/RouteTable/SG (но не для Gateway — там strict, см. TODO #6).
3. **CIDR overlap** = `FailedPrecondition`, не `InvalidArgument` (`e015191`).
4. **CIDR host-bits = 0** обязательно, sync через netip.Masked.
5. **Subnet immutable**: `v4_cidr_blocks/v6_cidr_blocks/network_id/zone_id` —
   reject в mask, silent ignore в full-PATCH (`8158a84`).
6. **Hard-delete, не soft** (`4e3e7ec`).
7. **Default SG создаётся в kacho-vpc-controllers**, не здесь (`c054750` —
   strip post-processing).
8. **Timestamp truncate to seconds** в proto-ответе (`ac61127`).
9. **DeletionProtection sync-check** перед Delete — `FailedPrecondition`
   `"... deletion_protection enabled"` (`333c535`).
10. **page_size валидируется**, page_token garbage → `InvalidArgument` (`5d16961`).

## 16. Ссылки

- Workspace правила: `../kacho-workspace/CLAUDE.md`
- Acceptance документ: `../kacho-workspace/docs/specs/sub-phase-0.3-vpc-acceptance.md`
- Proto: `../kacho-proto/proto/kacho/cloud/vpc/v1/`
- Outstanding TODO: `./TODO.md`
- Spec data model: `../kacho-workspace/docs/specs/02-data-model-and-conventions.md`

## 17. VPC-specific subagents

Помимо общих 13 агентов (acceptance-author/reviewer, proto-sync, ...),
в `.claude/agents/` есть 4 VPC-специализированных:

- `vpc-yc-parity-auditor` — аудит verbatim YC parity (regex, error texts,
  status codes, timestamp).
- `vpc-cidr-specialist` — CIDR-эксперт (host-bits, EXCLUDE, overlap, internal
  IP в CIDR).
- `vpc-outbox-watch-engineer` — outbox + LISTEN/NOTIFY + InternalWatchService
  + InternalAddressService.
- `vpc-newman-author` — Newman regression suites (quota-aware, 3-suite split).

Использовать после соответствующих этапов: yc-parity-auditor — после
rpc-implementer перед merge; cidr-specialist — при работе над Subnet/Address
CIDR features; outbox-watch-engineer — при изменении outbox/Watch logic;
newman-author — при добавлении нового RPC в e2e-coverage.
