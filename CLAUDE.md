# kacho-vpc — CLAUDE.md

VPC-специфичный CLAUDE.md, дополняющий общий workspace `CLAUDE.md` (лежит в
корне `kacho-workspace/`, подцепляется автоматически через parent-walkup
discovery Claude Code). Этот файл — обязательный контекст при работе из
`project/kacho-vpc/` и любых его подпапок.

## 1. Что это за сервис

Sub-phase 0.3 продукта Kachō. gRPC-сервис управления сетевой инфраструктурой:
**Network, Subnet, Address, RouteTable, SecurityGroup, Gateway, PrivateEndpoint**.

> **Verbatim-YC parity — ОТЛОЖЕНА** (см. workspace-`CLAUDE.md` «Что это за проект»). Раньше
> цель была — побайтовое соответствие Yandex Cloud VPC API (proto-форма, error texts, status
> codes, timestamp precision, regex'ы, behavioural semantics). Сейчас это **не constraint**:
> API можно проектировать в чистой форме, расходясь с YC где это лучше (например — `vpn_id`
> на Network, отдельный ресурс NetworkInterface AWS-ENI-стиля, и т.п.). YC-совместимость —
> отдельная поздняя фаза (compat-слой / рефакторинг). Существующие YC-точные детали ниже в
> этом файле (error texts, regex'ы, probe-результаты) сохраняются как есть, пока не решено
> иначе, но **расхождение с YC больше не баг** и не повод заводить issue.

Существующие/целевые домены: 7 verbatim-исторических ресурсов + (планируется, см. workspace
обсуждение) `vpn_id` на Network (internal) и, возможно, отдельный ресурс NetworkInterface.

В скоупе:
- 7 ресурсов с CRUD + Move + ListOperations.
- Subnet: AddCidrBlocks / RemoveCidrBlocks / Relocate / ListUsedAddresses.
- Address: GetByValue / ListBySubnet (для ref-validation в peer-сервисах).
- SecurityGroup: UpdateRules / UpdateRule + auto-default-on-network.
- Internal endpoints (на порту 9091, не выставляется на external TLS endpoint;
  часть проброшена через api-gateway на cluster-internal listener для UI/admin):
  `InternalWatchService` (outbox stream через LISTEN/NOTIFY), `InternalAddressService`
  (allocate/free internal/external IP — вызывается in-process из `AddressService.doCreate`
  и admin-tooling), `InternalAddressPoolService`, `InternalRegionService`,
  `InternalZoneService`, `InternalNetworkService`, `InternalCloudService` (IPAM/admin).
- IPAM allocate (cascade resolve + двухфазный аллокатор) — inline в request-path
  service-слоя (`address.go`); раньше — отдельный `kacho-vpc-controllers`, упразднён в Phase 2.
- Inline default-SG creation при Network.Create — управляется `KACHO_VPC_DEFAULT_SG_INLINE`
  (default `true`); раньше — reconciler в `kacho-vpc-controllers`.
- Outbox + LISTEN/NOTIFY для event stream.

Вне скоупа:
- Реальный data plane (это control plane only, как и весь Kachō).
- DNS-records для Address — proto-поля есть (`dns_record_specs`), но сервис их
  пока игнорирует (GitHub Issue, метка `blocked:kacho-dns`).

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
rewrite, в текущей схеме (`internal/migrations/0001_initial.sql`) их нет. `cloud_id` /
`organization_id` в схеме отсутствуют — фильтрация только по `folder_id`.

**FK contract:**
- Network → Subnet, RouteTable, SG: RESTRICT (нельзя удалить Network с детьми)
- Subnet → Address (internal): RESTRICT
- Network → default_security_group_id: ON DELETE SET NULL (default SG удаляется
  явно перед Network)

## 3. Resource ID format

Все ресурсы получают ID через `kacho-corelib/ids.NewID(<prefix>)`. Префиксы —
3 символа + 17-char crockford-base32. **Источник истины — `kacho-corelib/ids/ids.go`**:

| Ресурс           | Prefix const                | Значение | Пример                |
|------------------|-----------------------------|----------|------------------------|
| Network          | `ids.PrefixNetwork`         | `enp`    | `enp + 17 base32`      |
| Subnet           | `ids.PrefixSubnet`          | `e9b`    | `e9b + ...`            |
| Address          | `ids.PrefixAddress`         | `e9b`    | `e9b + ...`            |
| RouteTable       | `ids.PrefixRouteTable`      | `enp`    | `enp + ...`            |
| SecurityGroup    | `ids.PrefixSecurityGroup`   | `enp`    | `enp + ...`            |
| Gateway          | `ids.PrefixGateway`         | `enp`    | `enp + ...`            |
| PrivateEndpoint  | `ids.PrefixPrivateEndpoint` | `enp`    | `enp + ...`            |
| AddressPool      | литерал `"apl"` в `address_pool_service.go` | `apl` | `apl + ...`  |
| Operation (VPC)  | `ids.PrefixOperationVPC` (== `ids.PrefixNetwork`) | `enp` | `enp + ...` |

⚠️ Network/RouteTable/SecurityGroup/Gateway/PrivateEndpoint и Operation **делят `enp`**
— это умышленно: api-gateway маршрутизирует `OperationService.Get(id)` по первым
3 символам id, и все VPC-операции должны идти в один backend (`PrefixOperationVPC == PrefixNetwork`).
Subnet/Address — `e9b`. (Region/Zone — это строки вида `ru-central1`/`ru-central1-a`, не prefix-id.)

Колонки `id` — `TEXT` (исторически переход от UUID — миграция 0009; сейчас в squashed baseline).
**Не использовать UUID-валидацию на входе** — но каждый id-берущий RPC обязан первым стейтментом
вызвать `corevalidate.ResourceID(resourceType, ids.PrefixXxx, id)`: нераспознанный id (нет
известного 3-char prefix `b1g/bpf/enp/e9b/epd/fd8`) → sync `InvalidArgument "invalid <res> id '<X>'"`
(verbatim YC, probe 2026-05-11); well-formed-но-несуществующий (известный prefix) → `NotFound` через
`repo.Get`. Семантика family-agnostic (`enp...` как subnet-id проходит prefix-check → `repo.Get` →
`NotFound`, как реальный YC). (Старый совет «garbage-id всегда async NotFound», `ac61127`, устарел —
см. §15 gotcha #1 / `docs/architecture/06-conventions.md`.)

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
"google.protobuf.Empty"`. Worker возвращает `anypb.New(&emptypb.Empty{})`,
metadata уже в `Operation.metadata` (передана при `operations.New`). ✅ Реализовано
во всех 6 Delete-операциях (`network.go`/`subnet.go`/`address.go`/`route_table.go`/
`security_group.go`/`gateway.go`/`private_endpoint.go` — `return anypb.New(&emptypb.Empty{})`).

### 4.3 Outbox + LISTEN/NOTIFY

Каждая успешная мутация в `service/*.go` (через worker) пишет событие в
`vpc_outbox` в той же транзакции, что и сам ресурс. Триггер
`vpc_outbox_notify_trg` шлёт `pg_notify('vpc_outbox', sequence_no::text)`.

`InternalWatchHandler.Watch(req)` (`internal/handler/internal_watch_handler.go`):
1. Acquire per-stream semaphore slot (`KACHO_VPC_WATCH_MAX_STREAMS`, default 32) → иначе `ResourceExhausted`.
2. `pgx.Connect(ctx, cfg.MigrateDSN())` под inner timeout 2s — **dedicated conn вне pgxpool**
   (LISTEN не работает корректно на pooled conn; на abnormal exit conn просто закрывается).
3. `LISTEN vpc_outbox`.
4. Catchup: `SELECT * FROM vpc_outbox WHERE sequence_no > req.from_sequence_no` (batch 100, loop до < 100).
5. Loop: `conn.WaitForNotification(ctx)` (timeout 30s для periodic re-poll) → читать новые события → стрим клиенту.
6. `defer UNLISTEN vpc_outbox + conn.Close() + release semaphore slot`.

### 4.4 UpdateMask discipline

YC verbatim: каждое Update RPC принимает `google.protobuf.FieldMask`.

**Decision table:**
- mask содержит unknown поле → `InvalidArgument` (через `corevalidate.UpdateMask` с known-set).
- mask содержит **hard-immutable** поле → `InvalidArgument` (`"<field> is immutable after Xxx.Create"`).
- mask пустой → full-object PATCH: применяются все mutable поля; immutable из
  тела запроса **silently игнорируются** (это verbatim YC behaviour, см. `8158a84`).
- mask содержит mutable поле → применяется; валидируется по тем же правилам, что Create.

**Hard-immutable fields** (явно в mask → `InvalidArgument`):
- Subnet: `network_id`, `zone_id`
- Address: `external_ipv4_address_spec`, `internal_ipv4_address_spec`, `folder_id`
- остальные ресурсы: `folder_id` (управляется через Move)

**Soft-immutable** (в mask → НЕ ошибка; verbatim YC `200`; у нас — no-op): Subnet
`v4_cidr_blocks`/`v6_cidr_blocks` (YC принимает в mask и меняет CIDR; наш `repo.Update`
CIDR-колонки не перезаписывает → 200, но изменения нет — kacho-vpc#10, см. §12 / 07-known-divergences.md).
Реальное изменение CIDR — через `:add-cidr-blocks`/`:remove-cidr-blocks`.

Шаблон проверки в начале Subnet.Update:
```go
for _, field := range req.UpdateMask {
    switch field {
    case "network_id", "zone_id":
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
  `Description` (≤256), `Labels` (≤64 пар, key regex), `corevalidate.ZoneId`
  (**required-only** — hardcoded whitelist убран)
- ZoneId existence — sync, в `SubnetService.validateZoneID` через порт `ZoneRegistry`
  (запрос к таблице `zones`); неизвестная зона → `InvalidArgument`
- CIDR: `validateCIDRPrefix` (host-bits = 0)
- DhcpOptions: `domain_name` RFC 1123, `domain_name_servers[]` / `ntp_servers[]` IP
- UpdateMask: known-set + immutable check
- DeletionProtection: проверяется sync перед Delete (`333c535`)
- Address spec: oneof external/internal — exactly one

**Async (внутри Operation worker):**
- folder existence через `folderClient.Exists` → `NotFound`
- network existence (для дочерних ресурсов) → `NotFound`
- Repo Insert/Update — FK violations, EXCLUDE constraint (CIDR overlap),
  UNIQUE violation `(folder_id, name)` для всех 7 ресурсов (`networks_folder_id_name_key`
  non-partial; для остальных 6 — partial `WHERE name <> ''`, миграция `0002_resource_name_unique.sql`)
- Все эти ошибки маппятся через `mapRepoErr` в gRPC-status

## 6. Error mapping

`internal/service/network.go::mapRepoErr` — единая точка трансляции:

| Sentinel error           | gRPC code             | Verbatim YC text source           |
|--------------------------|-----------------------|------------------------------------|
| `ErrNotFound`            | `NOT_FOUND`           | `"<Resource> %s not found"` (Network/Subnet/Address/Gateway/PrivateEndpoint); `"Route table %s not found"`; `"Security group SecurityGroup.Id(value=%s) not found"` — verbatim YC, probe 2026-05-11, kacho-vpc#10. RT: repo передаёт `kind="Route table"`; SG: helper `wrapSGErr` в `security_group_repo.go`. |
| `ErrAlreadyExists`       | `ALREADY_EXISTS`      | `"<resource> with name ... exists"` |
| `ErrFailedPrecondition`  | `FAILED_PRECONDITION` | varies (`"Subnet CIDRs can not overlap"`, `"Invalid subnet state"`, `"network is not empty"`, ...) |
| `ErrInvalidArg`          | `INVALID_ARGUMENT`    | varies (`"Illegal argument Invalid network prefix /N"` для Subnet `>/28`, `"Illegal argument Destination folder is the same as the source"` для Move-в-текущий-folder, `"Invalid rule id <id>"` для SG.UpdateRule, CIDR host-bits, ...) |
| `ErrInternal`            | `INTERNAL`            | `"internal database error"` (no leak) |

`stripSentinel` удаляет префикс sentinel из текста, чтобы клиент видел verbatim
YC сообщение без internal-обёртки.

**Specific mappings:**
- CIDR overlap (PG SQLSTATE `23P01` от EXCLUDE constraint) → `FAILED_PRECONDITION`
  с текстом `"Subnet CIDRs can not overlap"` (см. `e015191`, `e43996b`).
- malformed / нераспознанный resource-id (нет известного 3-char prefix `b1g/bpf/enp/e9b/epd/fd8`) →
  sync `InvalidArgument "invalid <res> id '<X>'"` (`corevalidate.ResourceID`, первым стейтментом в
  каждом id-берущем RPC; verbatim YC, probe 2026-05-11). Well-formed-но-несуществующий (известный
  prefix) → `NotFound` через `repo.Get`. Семантика family-agnostic.
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

Verbatim YC (probe 2026-05-11, kacho-vpc#10): `Subnet.Relocate` **всегда** отвергается
синхронно — `FailedPrecondition "Invalid subnet state"`, даже для свежей подсети без
адресов и валидной целевой зоны. Operation **не создаётся**. (YC требует внутреннее
состояние подсети, которое control-plane без data-plane не моделирует.) Sync-проверки в
`SubnetService.Relocate`: формат id → валидность `destination_zone_id` → существование
подсети (`repo.Get` → `NotFound`) → `FAILED_PRECONDITION "Invalid subnet state"`.

## 9. Default Security Group

Управляется флагом `KACHO_VPC_DEFAULT_SG_INLINE` (envconfig, default `true`).
`cmd/vpc/main.go` вызывает `networkSvc.SetSGRepo(sgRepo)` только при `=true`.

**`KACHO_VPC_DEFAULT_SG_INLINE=true` (default)** — при Network.Create:
1. SYNC создаётся Operation, возвращается клиенту.
2. ASYNC в worker (`network.go::doCreate`): `repo.Insert(network)` →
   inline создаётся default SG `default-sg-{first-8-chars-of-net-id}` с
   default-правилами → `UPDATE networks SET default_security_group_id = sg.id`
   (всё в TX'ах worker'а) → outbox-события Network.CREATED / SecurityGroup.CREATED / Network.UPDATED.

**`KACHO_VPC_DEFAULT_SG_INLINE=false`** — Network.Create НЕ создаёт SG,
`default_security_group_id` остаётся пустым; создание делегируется внешнему
reconciler'у. Убирает 2 INSERT + 1 UPDATE из hot-path (≈ +30-40% write-throughput)
— для load-тестов / deploy с внешним SG-reconciler'ом. В таком режиме
newman-кейсы `*-LSG-CRUD-DEFAULT-SG` / `*-DEL-STATE-DEFAULT-SG` краснеют.

(Раньше default-SG создавал отдельный `kacho-vpc-controllers` reconciler — упразднён в Phase 2.)

При Network.Delete: worker сначала удаляет default SG (если есть), потом Network.
Не-default SG препятствуют удалению (FK RESTRICT) → клиент получает
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

**Боевые миграции** — в `internal/migrations/*.sql`, embedded через `embed.FS`
(`migrations.go`). Физически **два файла** — 22 исторические миграции свёрнуты
(commit `5581316`, `refactor(vpc): inline AddressAllocator + squash 22 миграции`)
в один baseline:
- `0001_initial.sql` — squashed baseline: все таблицы (`operations`, `networks`,
  `subnets`, `addresses`, `route_tables`, `security_groups`, `gateways`,
  `private_endpoints`, `regions`, `zones`, `address_pools`, binding-таблицы,
  `cloud_pool_selector`, `vpc_outbox`, `vpc_watch_cursors`), индексы,
  EXCLUDE/UNIQUE constraints, generated columns, outbox trigger. Id-колонки — `TEXT`.
  `networks_folder_id_name_key` — non-partial UNIQUE `(folder_id, name)`.
- `0002_resource_name_unique.sql` — partial UNIQUE `(folder_id, name) WHERE name <> ''`
  для `subnets`/`route_tables`/`security_groups`/`gateways`/`private_endpoints`/`addresses`
  (закрыл расхождение с verbatim YC — раньше UNIQUE был только у Network; commit `ee07a7e`).

`migrations/` (в корне репо) — staging для `make sync-migrations` (только
`0001_operations.sql` от corelib; в `0001_initial.sql` схема `operations` уже включена).
Source of truth — `internal/migrations/`. Историческая нумерация (0001–0022 до squash)
встречается в комментариях/docs как «(миграция 0007)» — это происхождение DDL,
физически всё в `0001_initial.sql`. Актуальный state БД — `goose_db_version`.

`migrate` использует `cfg.MigrateDSN()` (без `pool_max_conns` — иначе `database/sql`
шлёт серверу unknown PG-param → `FATAL`, см. FINDING-007); pgxpool использует `cfg.DSN()`
(с `pool_max_conns` если `KACHO_VPC_DB_MAX_CONNS > 0`).

**Запреты:**
- НЕ редактировать применённые миграции (`0001_initial.sql`, `0002_*`) (запрет #5 из workspace CLAUDE.md).
- НЕ модифицировать `0001_operations.sql` (staging-копия из corelib).
- Новая миграция = новый файл с инкрементным номером (следующий — `0003_*`).

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

# Newman regression (нужен port-forward api-gateway → localhost:18080)
python3 tests/newman/scripts/gen.py            # перегенерить коллекции из cases/*.py
tests/newman/scripts/run.sh                    # все сервисы целиком; --service network для одного
tests/newman/scripts/run-incremental.sh        # ПО ОДНОМУ кейсу за раз + зачистка после каждого (quota-safe; --resume / --cleanup-only)
# против реального YC (parity-аудит — всё ≠ YC = баг): node tests/newman/scripts/yc-proxy.js & ;
#   ENV=tests/newman/environments/yc.postman_environment.json SERVICES='<8 public, без internal-*>' tests/newman/scripts/run-incremental.sh
```

## 14. Тесты

Три уровня тестирования; за каждый отвечает отдельная инфраструктура.

### 14.1 Unit (`internal/service/*_test.go`, `internal/handler/*_test.go`)

Моки port-интерфейсов — из общего пакета `internal/ports/portmock` (раньше
каждый test-файл держал свою копию). Worker-горутины `operations.Run`
дожидаются детерминированно через `portmock.AwaitOpDone` / `AwaitAllOpsDone`
(poll до `Operation.Done` с дедлайном 2s — не фиксированный `time.Sleep`).
Запуск: `make test-short` (или `go test ./... -short`).

Если service-тест требует Postgres → это сигнал об утечке adapter в
use-case. См. `go-style-reviewer §3.11`.

### 14.2 Integration (`internal/repo/*integration_test.go`)

Testcontainers с Postgres 16. Гоняется и локально (`make test`), и **в CI**
— job `integration` в `.github/workflows/ci.yaml` (`go test ./internal/repo/...
-race -count=1`; на ubuntu-runner'ах Docker есть). Покрывает:
- Repo CRUD против реальной БД.
- EXCLUDE constraint поведение (CIDR overlap → 23P01).
- FK violations (Network с детьми → 23503).
- UNIQUE violations (duplicate name → 23505).
- Outbox emit транзакционность; LISTEN/NOTIFY (`internal_watch_integration_test.go`).
- SecurityGroup OCC через `xmin` (`security_group_occ_integration_test.go`).
- IPAM cascade pool-resolve (`ipam_cascade_integration_test.go`).

### 14.3 E2E / Postman (`tests/newman/`)

**Главная regression-инфраструктура** — black-box покрытие всех публичных RPC
(спроектировано по `testing-product-coach`/`testing-code-coach`). Только HTTP
через api-gateway (`localhost:18080`, port-forward). Декларативный генератор:
case-файлы на Python → `gen.py` → Postman-коллекции по сервису.

```
tests/newman/
├─ cases/                       — декларативные case-наборы по сервису (Python; ИСТОЧНИК ИСТИНЫ)
│  ├─ network.py / subnet.py / address.py / route-table.py /
│  │  security-group.py / gateway.py / private-endpoint.py / operation.py    — публичные RPC
│  └─ internal-pool.py / internal-region-zone.py / internal-cloud.py         — admin/IPAM RPC
├─ collections/                 — СГЕНЕРИРОВАННЫЕ Postman-коллекции (по сервису) — НЕ править руками
│  └─ {…}.postman_collection.json
├─ environments/{local,yc}.postman_environment.json   — local stand (18080) / реальный YC (через yc-proxy :18081)
├─ scripts/
│  ├─ gen.py                    — генератор коллекций из cases/*.py
│  ├─ run.sh                    — прогон одного / всех сервисов целиком (newman + JSON reporter → out/)
│  ├─ run-incremental.{sh,js}   — прогон по одному кейсу за раз + зачистка ресурсов (quota-safe; --resume / --cleanup-only; env SERVICES=...)
│  └─ yc-proxy.js               — reverse-proxy для прогона против реального YC (vpc.api / operation.api + Bearer из `yc iam create-token`)
├─ docs/
│  ├─ TAXONOMY.md               — классы кейсов + naming convention (CRUD/VAL/NEG/BVA/CONF/STATE/...)
│  ├─ TEST-PLAN.md              — карта покрытия (RPC × класс)
│  ├─ CASES-INDEX.md            — каталог уникальных паттернов кейсов
│  ├─ PRODUCT-REQUIREMENTS.md   — НОРМАТИВНЫЙ регламент REQ-* (от QA; выведен из CASES-INDEX; vpc-yc-parity-auditor проверяет соответствие)
│  ├─ REQUIREMENTS.md           — бэклог улучшений (testability/contract-clarification — не нормативный)
│  └─ RESULTS.md                — последний прогон pass/fail + история версий + skill-mapping
└─ out/                         — newman raw output + summary.txt (gitignored snap-логи)
```
(Найденные дефекты/наблюдения — НЕ здесь, а GitHub Issues, см. §14.4.)

**Запуск:**
```bash
python3 tests/newman/scripts/gen.py            # перегенерить ВСЕ коллекции (или: gen.py network)
tests/newman/scripts/run.sh                    # все сервисы; сводка в out/summary.txt
tests/newman/scripts/run.sh --service network  # один сервис
tests/newman/scripts/run.sh --service subnet --delay 60 --bail
```

**Контракт изоляции кейса**: каждый case — внутри своего `runId`; suite работает
внутри pre-allocated `existingFolderId`/`existingFolderCrossId` (из env), Org/Cloud/Folder
**не создаёт**. Имена ресурсов суффиксуются `{{runId}}`. Полный case-id:
`<DOMAIN>-<ACTION>-<DETAIL>`, например `NET-CR-CRUD-OK`, `SUB-CR-NEG-DUP-NAME`.

**Требует `KACHO_VPC_DEFAULT_SG_INLINE=true`** (default) — кейсы `*-LSG-CRUD-DEFAULT-SG` /
`*-DEL-STATE-DEFAULT-SG` проверяют авто-создание default SG. При `=false` (load-test
config) эти кейсы краснеют.

Текущий результат: ~731 кейс / 3361 assertions / 0 fail (v16; см. `tests/newman/docs/RESULTS.md`).
Подробности добавления нового кейса, разрешённых паттернов и common pitfalls
см. в агенте `vpc-newman-author`. (Старая quota-aware 3-suite сьюта против реального
YC API — `newman_legacy/` — удалена; история — в git.)

### 14.4 Где фиксировать найденные баги и задачи (ОБЯЗАТЕЛЬНО)

**Любой баг / расхождение с verbatim YC / observability-gap / доп-задача, найденная в
newman, k6, integration- или unit-тестах (или при ревью/прогоне) → GitHub Issue в
`PRO-Robotech/kacho-vpc`** (если не VPC-specific, а общий — в `PRO-Robotech/kacho-workspace`).
**`TODO.md` упразднён** — теперь это stub со ссылкой на Issues; источник истины «что надо
сделать» — открытые issues, не файлы в репо. (Полная конвенция — workspace `CLAUDE.md` →
«Баги, задачи, tech-debt — GitHub Issues» + «Кросс-репо зависимости и порядок выполнения».)

- Метки: `bug` / `tech-debt` / `enhancement`. Заблокировано ещё-не-реализованным сервисом →
  `blocked:kacho-dns` / `blocked:kacho-iam` + в теле issue «при каких условиях браться».
  Кросс-репо эпик → tracking-issue в `kacho-workspace` (метка `epic`), per-repo issue помечает
  `Blocked by PRO-Robotech/<repo>#<n>`.
- Коммит, закрывающий issue — trailer `Closes #N` (или `Closes PRO-Robotech/<repo>#N` для кросс-репо).
- В тест-кейсе допустима короткая аннотация `# verifies <короткое описание>` (можно со ссылкой
  на issue) — но **не** дублирование описания бага.
- **Не баг** (by-design / documented divergence с verbatim YC) → **не issue**, а запись в
  `docs/architecture/07-known-divergences.md`. Отдельных bug-map'ов / FINDING-NNN-реестров — не вести.
- **Новое продуктовое требование** (что продукт ДОЛЖЕН делать, выявлено из тест-анализа) → новый `REQ-*` в
  `tests/newman/docs/PRODUCT-REQUIREMENTS.md` (нормативный регламент от QA; на соответствие ему проверяет
  агент `vpc-yc-parity-auditor` §3.13 при ревью изменений). Каждый newman-кейс мапится на `REQ-*`.

## 15. Top-10 gotchas (из истории фиксов)

1. **id sync-валидация** — malformed / нераспознанный resource-id (нет известного 3-char prefix `b1g/bpf/enp/e9b/epd/fd8`) → sync `InvalidArgument "invalid <res> id '<X>'"` (`corevalidate.ResourceID`, вызывается первым стейтментом в каждом id-берущем RPC: Get/Update/Delete/Move/AddCidrBlocks/RemoveCidrBlocks/Relocate/UpdateRules/UpdateRule/ListXxx-by-parent/Create-parent-network; verbatim-YC, probe 2026-05-11). Well-formed-но-несуществующий id (известный prefix) → `NotFound` через `repo.Get`. Семантика family-agnostic — `enp...` как subnet-id проходит prefix-check, затем `repo.Get` → `NotFound` (как реальный YC). (`kacho-vpc#7` — закрыт; старый gotcha «не валидировать id sync», `ac61127`, устарел и заменён.)
2. **NameVPC permissive, не strict** — empty/uppercase/underscore разрешены для
   Network/Subnet/Address/RouteTable/SG. Gateway — отдельный strict-контракт
   `corevalidate.NameGateway` (lowercase, без uppercase/underscore — verbatim YC).
3. **CIDR overlap** = `FailedPrecondition`, не `InvalidArgument` (`e015191`).
4. **CIDR host-bits = 0** обязательно, sync через netip.Masked.
5. **Subnet immutable**: `network_id/zone_id` hard-reject в mask; `v4_cidr_blocks/v6_cidr_blocks`
   в mask — НЕ ошибка (verbatim YC `200`, у нас no-op — kacho-vpc#10); все они silent-ignore в
   full-PATCH (`8158a84`, см. §4.4).
6. **Hard-delete, не soft** (`4e3e7ec`).
7. **Default SG создаётся inline в `network.go::doCreate`** при `KACHO_VPC_DEFAULT_SG_INLINE=true`
   (default; `cmd/vpc/main.go` вызывает `SetSGRepo` только при этом флаге). Раньше — reconciler
   в `kacho-vpc-controllers` (упразднён в Phase 2). См. §9.
8. **Timestamp truncate to seconds** в proto-ответе (`ac61127`).
9. **DeletionProtection sync-check** перед Delete — `FailedPrecondition`
   `"... deletion_protection enabled"` (`333c535`).
10. **page_size валидируется**, page_token garbage → `InvalidArgument` (`5d16961`).

## 16. IPAM (built-in, internal-only)

IPAM встроен в kacho-vpc (Phase 2: `kacho-vpc-controllers` упразднён, allocate —
inline в `address.go`). Внешней зависимости от NetBox нет. Схема — в squashed
`internal/migrations/0001_initial.sql` (исторически миграции 0014–0022). Этот раздел
описывает архитектуру; «(миграция NNNN)» ниже — происхождение DDL до squash.

### 16.1 Resources

- **AddressPool** — **глобальный
  infrastructure-ресурс** (как Region/Zone). Не привязан к Org/Cloud/Folder —
  пулы общие на всю инсталляцию. Admin-only resource, **нет в публичном VPC
  API** (verbatim YC: такого resource в YC нет; kacho-only). Управляется через
  `kacho.cloud.vpc.v1.InternalAddressPoolService` gRPC; exposed на
  cluster-internal порту 9091 + проброшен через api-gateway REST mux на
  `/vpc/v1/addressPools/...` (для UI/admin-tooling). На external TLS endpoint
  (`api.kacho.local:443`, advertised для `yc` CLI) этот path **не должен быть
  доступен** — внутренние служебные сущности наружу не публикуются.
  - Поля: `id`, `name`, `description`, `labels`, `cidr_blocks`,
    `kind` (EXTERNAL_PUBLIC/EXTERNAL_TEST/RESERVED_INTERNAL), `zone_id` (FK
    к `zones`, NULL = глобальный пул), `is_default`, **`selector_labels`**,
    **`selector_priority`**.
  - **Нет** `folder_id` (миграция 0021 убрала). Address из любого folder в
    указанной zone берёт IP из единого глобального pool этой zone.
  - В пределах `(zone_id, kind)` допускается ровно один `is_default=true`
    (DB-level partial UNIQUE с `COALESCE(zone_id, '')`, миграция 0020).
  - **Region/Zone** — ⚠️ **переносятся из kacho-vpc в kacho-compute** (эпик
    `KAC-15`; см. workspace CLAUDE.md §«Кросс-доменные ссылки на ресурсы» —
    Geography = домен compute). После переноса: таблиц `regions`/`zones` в схеме
    `kacho_vpc` **нет**; `Address.external_ipv4_spec.zone_id` и `AddressPool.zone_id`
    хранят zone-id как `TEXT` **без FK**; существование `zone_id` валидируется на
    request-path вызовом `compute.v1.ZoneService.Get` через
    `internal/clients/compute_client.go` (port `GeographyRegistry` в `service/`);
    несуществующая зона → `InvalidArgument` на Create, грациозная деградация при
    чтении уже сохранённых ресурсов; `InternalRegionService`/`InternalZoneService`
    из vpc-пакета **удалены** — admin-CRUD geography теперь `/compute/v1/regions`,
    `/compute/v1/zones`. (До merge'а KAC-15 — старая схема: миграция 0019, таблицы
    `regions`/`zones`, `InternalRegionService`/`InternalZoneService` на
    `/vpc/v1/regions`,`/vpc/v1/zones`.)

### 16.x Публикация admin-only ресурсов

Three admin-only ресурса (Region, Zone, AddressPool) проброшены через
`api-gateway` REST mux на cluster-internal listener для UI и admin-tooling.
**Требование:** на external TLS endpoint (advertised `api.kacho.local:443`
для `yc` CLI и других внешних клиентов) эти пути НЕ ДОЛЖНЫ быть доступны.

Текущая реализация: api-gateway имеет два listener'а — plain `:8080` (cluster
ingress / UI) и опциональный TLS `:TLS_LISTEN_ADDR` (для yc-CLI compat). Оба
сейчас обслуживают один и тот же mux. Когда TLS-listener будет включён в
production-конфиге, нужно прикрутить admin-paths block на TLS-listener.
Реализация: middleware на TLS-cmux, отвечающий 404 на пути:
- `/vpc/v1/regions*`
- `/vpc/v1/zones*`
- `/vpc/v1/addressPools*`
- `/vpc/v1/networks/*/addressPoolBinding`
- `/vpc/v1/addresses/*/addressPoolOverride`

При добавлении новых admin-only RPC — обновить этот список.

### 16.y Куда добавлять новые admin-RPC

Если admin-UI требует данные/действия, которых нет в публичном (verbatim-YC) API:

1. **НЕ расширять публичный сервис** (`NetworkService`, `SubnetService`,
   `AddressService`, `AddressPoolService` — последний у нас уже internal,
   но шаблон тот же). Это сломает verbatim-YC parity и засветит admin
   функционал на external TLS endpoint.
2. Добавлять новый RPC в **существующий `Internal*` сервис** соответствующего
   домена (например, `InternalAddressPoolService.GetUtilization` — admin
   observability для уже internal-only ресурса).
3. Если подходящего internal-сервиса нет — создать новый
   `internal_<domain>_service.proto` с `kacho-only`-disclaimer'ом в
   header-комментарии.
4. Аннотировать `google.api.http` для REST доступа из UI.
5. Зарегистрировать в `kacho-api-gateway/internal/restmux/mux.go` под блоком
   `if vpcInternalAddr != ""` — это автоматически попадает только на
   cluster-internal listener.
6. На external TLS endpoint этот путь НЕ должен быть доступен — добавить в
   список §16.x при включении production-TLS-фильтра.

- **NetworkPoolSelector** (миграция 0016, отдельная таблица
  `network_pool_selector`) — admin-controlled routing-labels for Network.
  **Не путать** с `Network.labels` (public YC API field):
  - `Network.labels` — user-controlled, для поиска ресурса.
  - `NetworkPoolSelector.selector` — admin-controlled, для системного pool
    routing. Хранится отдельно (не загрязняет flat-schema networks).
  - Управляется через `InternalNetworkService.{Set,Unset,Get}PoolSelector`.

### 16.2 Cascade резолва pool при `AllocateExternalIP`

```
1. address_pool_address_override[address_id]                  (explicit per-address)
2. address_pool_network_default[network_id]                   (explicit per-network)
3. label-selector match через NetworkPoolSelector:
     SELECT id FROM address_pools
       WHERE selector_labels @> $network_selector       -- pool ⊇ network (containment)
         AND selector_labels <> '{}'
         AND (zone_id = $address_zone OR zone_id IS NULL)
         AND kind = $kind
       ORDER BY
         (jsonb_object_size(selector_labels) - $network_size) ASC,  -- diff: точнее = выигрывает
         selector_priority DESC                                       -- explicit tie-break
       LIMIT 1;
4. zone_default        (is_default=true для zone+kind)
5. global_default      (is_default=true для zone IS NULL и kind)
```

### 16.3 Match-семантика (нормативно)

**Inverse vs k8s NodeSelector**: `network_pool_selector ⊆ pool.selector_labels`
(pool описывает **whitelist разрешённых labels**). Если у network есть label,
не упомянутый в pool — она **не** match'ается с этим pool через label-cascade.
Это **safe-by-default**: неучтённая комбинация labels попадает в default-pool,
а не в специальный через subset-trick.

| Pool requires | Network selector | Match? |
|---|---|---|
| `{tier=premium}` | `{tier=premium}` | ✅ |
| `{tier=premium}` | `{tier=premium, customer=acme}` | ❌ (customer не упомянут в pool) |
| `{tier=premium, customer=acme}` | `{tier=premium}` | ✅ (network ⊆ pool) |
| `{tier=premium, customer=acme}` | `{tier=premium, customer=acme, env=prod}` | ❌ (env не упомянут) |

### 16.4 Tie-break при equal-diff и equal-priority

**Resolve order undefined.** Postgres вернёт первую row в physical order.
Это сознательный design choice — admin отвечает за избежание ambiguity.

Для обнаружения ambiguous конфигураций — `InternalAddressPoolService.Check`
(`GET /vpc/v1/addressPools:check?zoneId=...` через api-gateway internal mux) —
возвращает list of warnings.

Пример ambiguous:
```
2 pools share identical (zone_id, kind, selector_labels, selector_priority):
  pool apl-xxx "premium-base"   selector={tier=premium} priority=100
  pool apl-yyy "premium-clone"  selector={tier=premium} priority=100
→ resolve order undefined; set distinct selector_priority to disambiguate.
```

### 16.5 Allocate flow

`InternalAddressService.AllocateExternalIP(address_id)`:
1. Lookup address.
2. Idempotency: если `external_ipv4.address` уже заполнен — return existing
   с `already_allocated=true`.
3. `ResolvePoolForAddress` (см. cascade выше).
4. Linear sweep по `pool.cidr_blocks`: random IP + Update address с retry на
   UNIQUE-violation (constraint `addresses_external_pool_ip_uniq`).
5. Return allocated IP + `pool_id` (для observability — какой pool сработал).

### 16.6 Архитектурные правила

- AddressPool — **internal-only**. На external TLS endpoint не публикуется;
  проброшен через api-gateway на cluster-internal listener (`/vpc/v1/addressPools/...`)
  для UI/admin-tooling.
- `AllocateInternalIP` / `AllocateExternalIP` — **internal-only**. Вызываются
  in-process из `AddressService.doCreate` (inline allocate в request-path; раньше —
  `kacho-vpc-controllers`, упразднён в Phase 2) и admin-tooling.

### 16.7 Admin workflow (через api-gateway internal mux — нет CLI; curl/UI)

`kachoctl-ipam` CLI **удалён** — admin-операции делаются REST-запросами на
cluster-internal listener api-gateway (`http://api-gateway.kacho:8080`, локально —
port-forward на 18080) либо через web-UI.

```bash
BASE=http://localhost:18080   # port-forward api-gateway

# 1. Default-pool на zone (глобальный — без folder_id)
curl -XPOST $BASE/vpc/v1/addressPools -H 'content-type: application/json' -d \
  '{"name":"default-zone-a","kind":"EXTERNAL_PUBLIC","zoneId":"ru-central1-a","cidrBlocks":["198.51.100.0/24"],"isDefault":true}'

# 2. Специальный pool для tier=premium (selector — whitelist labels Network'а)
curl -XPOST $BASE/vpc/v1/addressPools -H 'content-type: application/json' -d \
  '{"name":"premium-pool","kind":"EXTERNAL_PUBLIC","zoneId":"ru-central1-a","cidrBlocks":["203.0.113.0/24"],"selectorLabels":{"tier":"premium"},"selectorPriority":100}'

# 3a. Привязать Cloud к premium routing (admin-controlled selector — cascade Step 3)
curl -XPOST $BASE/vpc/v1/clouds/<cloud-id>/poolSelector -H 'content-type: application/json' -d \
  '{"selector":{"tier":"premium"},"setBy":"admin@kacho"}'   # InternalCloudService.SetPoolSelector
# 3b. (либо явная per-network/per-address привязка):
curl -XPOST $BASE/vpc/v1/networks/<network-id>/addressPoolBinding -d '{"poolId":"apl..."}'
curl -XPOST $BASE/vpc/v1/addresses/<address-id>/addressPoolOverride -d '{"poolId":"apl..."}'

# 4. «Куда попадёт?» — InternalAddressPoolService.ExplainResolution
curl "$BASE/vpc/v1/addressPools:explainResolution?addressId=<address-id>&networkId=<network-id>"

# 5. Аудит ambiguous конфигов — InternalAddressPoolService.Check
curl "$BASE/vpc/v1/addressPools:check?zoneId=ru-central1-a"
```

## 17. Ссылки

- Workspace правила: `../../CLAUDE.md`
- Acceptance документ: `../../docs/specs/sub-phase-0.3-vpc-acceptance.md`
- Proto: `../kacho-proto/proto/kacho/cloud/vpc/v1/`
- Открытые задачи / баги / tech-debt: GitHub Issues — github.com/PRO-Robotech/kacho-vpc/issues
  (`TODO.md` упразднён, см. §14.4). Известные by-design расхождения с verbatim YC: `docs/architecture/07-known-divergences.md`
- Spec data model: `../../docs/specs/02-data-model-and-conventions.md`

## 18. VPC-specific subagents

Помимо общих 13 агентов (acceptance-author/reviewer, proto-sync, ...),
в `.claude/agents/` есть VPC-специализированные:

**Domain-experts (5):**

- `vpc-yc-parity-auditor` — аудит verbatim YC parity (regex, error texts,
  status codes, timestamp).
- `vpc-cidr-specialist` — CIDR-эксперт (host-bits, EXCLUDE, overlap, internal
  IP в CIDR).
- `vpc-outbox-watch-engineer` — outbox + LISTEN/NOTIFY + InternalWatchService
  + InternalAddressService.
- `vpc-newman-author` — newman regression suites (декларативные `cases/*.py` → `gen.py`).
- `vpc-load-testing` — нагрузочные сценарии VPC (k6 + ghz Jobs, см. `tests/k6/`; defers
  generic-методологию `load-testing-coach`).

**Testing coaches / load (skills в `.claude/skills/`):**

- `testing-code-coach` (`.claude/agents/TESTING.md`) — эталонные практики тестирования кода:
  test pyramid, AAA, fakes vs mocks, table-driven, property-based, mutation/fuzz,
  13 анти-паттернов, чек-листы. Применять при дизайне unit/integration/contract
  тестов и при review test-патча. Полное knowledge body — внутри файла.
- `testing-product-coach` (`.claude/agents/TESTING-PRODUCT.md`) — практики тестирования продукта
  как чёрного ящика: 11 формальных техник (ECP, BVA, decision tables, state
  transition, pairwise, use-case, error guessing, exploratory, property-based,
  risk-based, conformance), 14 типов тестов, метрики покрытия для black-box,
  применение к `tests/newman/` suite + заведение находок в GitHub Issues (§14.4).
- `vpc-load-testing` (skill) + `load-testing-coach` (workspace skill) — нагрузочное тестирование.

Использовать после соответствующих этапов: yc-parity-auditor — после
rpc-implementer перед merge; cidr-specialist — при работе над Subnet/Address
CIDR features; outbox-watch-engineer — при изменении outbox/Watch logic;
newman-author — при добавлении нового RPC в e2e-coverage; testing-code-coach —
при review тестов кода; testing-product-coach — при дизайне Newman-кейсов
и risk-prioritization фичи.
