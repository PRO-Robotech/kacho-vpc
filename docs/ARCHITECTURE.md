# kacho-vpc — итоговый архитектурный документ

Документ описывает сервис `kacho-vpc` в его текущем виде с детализацией,
достаточной для повторного воспроизведения. Изложение идет сверху вниз:
от системного контекста к компонентному уровню, далее — к поведенческим
паттернам, доменной модели, БД-схеме, API-поверхности, операционным
аспектам и шагам пересборки.

Стиль примеров — текст, таблицы и sequence-диаграммы. Кода в документе нет.

---

## Часть I. Системный контекст (C0–C1)

### 1.1 Назначение сервиса

`kacho-vpc` — control-plane сервис управления виртуальной сетью облачной
платформы Kachō. Он владеет жизненным циклом семи публичных доменных
ресурсов (Network, Subnet, Address, **NetworkInterface** — first-class
самостоятельный сетевой интерфейс, отдельный от Instance — RouteTable,
SecurityGroup, Gateway) и встроенным IPAM (AddressPool плюс network-default
binding; Region/Zone — leaf-домен `kacho-geo`, в VPC только `zone_id`-ссылка без FK).
Сервис **control-plane only**: он хранит конфигурацию,
валидирует ее и эмитит события об изменениях.
API проектируется в чистой форме под задачу: NIC как отдельный ресурс,
опциональные `Subnet` CIDR / `SecurityGroup` network и т.п.

### 1.2 Место в системе Kachō

Kachō — polyrepo. Каждый домен живет в отдельном Go-репозитории. Внешние
клиенты ходят через `kacho-api-gateway` (gRPC-proxy + grpc-gateway REST).
Сервисы общаются по gRPC. У каждого — своя Postgres-БД, шаринг через
прямой SQL запрещен.

```
                           kacho-ui (SPA, REST/JSON)
                                   |
                                   v
                         kacho-api-gateway
                          /              \
                         v                v
              kacho-iam               kacho-vpc
                  (Account/Project)    (этот сервис)
                         ^                ^
                         |                |
                         +-- gRPC ref-validation
```

`kacho-vpc` зависит от `kacho-iam` через порт-интерфейс
(`ProjectClient`) — проверяет существование project (DB-колонка `project_id` =
id владельца-проекта) и достает `account_id` (parent-scope) для
IPAM-cascade. Никакой прямой доступ к чужой БД.

### 1.3 Соседи и контракты

| Сосед | Канал | Что делает |
|---|---|---|
| `kacho-api-gateway` | gRPC `:9090` → REST | Маршрутизирует публичные RPC, преобразует ошибки в HTTP-status |
| `kacho-iam` | gRPC client | `ProjectClient.Exists(projectID)`, `ProjectClient.GetCloudIDFromProject(projectID)` (project existence + account-id lookup; `projectID` = id владельца-проекта) |
| `kacho-compute`, прочие IP-потребители | gRPC `:9091` | `InternalAddressService.AllocateInternalIP` / `AllocateInternalIPv6` / `AllocateExternalIP` + referrer-tracking; валидация NIC-spec (Subnet/SG) |
| `kacho-geo` (Geography owner, leaf) | gRPC client (исходящий) | `geo.v1.ZoneService.Get` — валидация `zone_id` (Region/Zone — leaf-домен geo) |
| Внутрикластерные потребители событий | Postgres `LISTEN/NOTIFY` (`vpc_outbox`) | Транзакционный outbox-журнал доменных мутаций; публичного Watch RPC нет — клиенты узнают об изменениях через polling `List` / `OperationService.Get` |
| Postgres (своя БД `kacho_vpc`) | pgx + LISTEN/NOTIFY | Источник истины |
| Admin-инструменты (UI, curl/REST на api-gateway internal mux) | gRPC `:9091` через api-gateway internal listener | Управление AddressPool; admin-операции Network (default-SG setter) |

### 1.4 Внешний контракт

Все мутации (`Create/Update/Delete/AddCidrBlocks/...`) возвращают
`Operation` (long-running async). Клиент поллит `OperationService.Get(id)`
до `done=true`. Все чтения (`Get/List/...`) — синхронные.

Ошибки маппятся в стандартные gRPC-коды с каноническим текстом контракта:
`NOT_FOUND "Project %s not found"`, `FAILED_PRECONDITION
"Subnet CIDRs can not overlap"`, и так далее.

### 1.5 Нефункциональные требования

| Свойство | Значение |
|---|---|
| Идемпотентность чтений | Полная (read-only, без побочных эффектов) |
| Идемпотентность мутаций | По `Operation.id`; повторный INSERT не делается |
| Изоляция БД-уровня | Read Committed (по умолчанию pg), критичные участки полагаются на EXCLUDE/UNIQUE |
| Стабильность контракта | Фиксированные regex, status codes и error texts — часть контракта, меняются осознанно |
| Graceful shutdown | До 30 секунд на drain LRO-worker'ов |
| Latency бюджет | Не зафиксирован формально; sync-валидация в request-path, async-IO в worker |
| Наблюдение состояния | Polling: `OperationService.Get` (результат мутации) + периодический `List` (Watch RPC не существует) |

---

## Часть II. Контейнерный уровень (C2)

### 2.1 Процессы

Один Go-бинарь `vpc` с двумя командами:

```
vpc migrate {up|down|status}    — применение/откат миграций
vpc serve                        — запуск gRPC-серверов
```

`serve` поднимает в одном процессе:

- gRPC-сервер на публичном порту (по умолчанию `:9090`).
- gRPC-сервер на internal-порту (по умолчанию `:9091`).
- Воркеров операций `kacho-corelib/operations.Run` — одна горутина на каждую
  in-flight LRO; пул не явный.
- Подключение к Postgres через `pgxpool` (один пул).
- FGA register-drainer (`kacho-corelib/outbox/drainer`) — слушает
  `kacho_vpc_fga_register_outbox` и применяет owner-tuple intents через `kacho-iam`.

### 2.2 Хранилище

База `kacho_vpc` в Postgres 16 (`btree_gist` обязательно). Схема `public`.
Один пул на весь процесс. Никакого второго хранилища (kv-store, queue,
external cache) — outbox реализован транзакционно поверх pg.

### 2.3 Деплоймент-вид

```
+-----------------------------------------------------------+
| Kubernetes namespace `kacho`                              |
|                                                           |
|  +------------------------+    +-----------------------+  |
|  | Deployment vpc         |    | StatefulSet vpc-db    |  |
|  | replicas: N            |    | (Postgres 16)         |  |
|  | container: vpc serve   |    |                       |  |
|  | ports: 9090, 9091      |    |                       |  |
|  +-----------+------------+    +----------+------------+  |
|              |                            |               |
|              | gRPC               pgx     |               |
|              v                            v               |
|  +-----------+------------+    +----------+------------+  |
|  | Service vpc-public     |    | Service vpc-db        |  |
|  | port 9090              |    | port 5432             |  |
|  +-----------+------------+    +-----------------------+  |
|              ^                                            |
|              |                                            |
|  +-----------+------------+    +-----------------------+  |
|  | Service vpc-internal   |    | NetworkPolicy         |  |
|  | port 9091 (cluster-IP) |    | allow :9091 from      |  |
|  +------------------------+    | api-gateway (admin    |  |
|                                | mux), kacho-compute   |  |
|                                +-----------------------+  |
+-----------------------------------------------------------+
```

API-gateway тянет публичный сервис на :9090; часть `Internal*`-RPC
(AddressPool — admin/IPAM) проброшена тем же
api-gateway на cluster-internal listener (`/vpc/v1/{addressPools,...}`).
Internal listener :9091 закрыт NetworkPolicy и не виден на external TLS endpoint.
IPAM-allocate и default-SG creation выполняются inline в service-слое
(отдельного reconciler-процесса нет).

### 2.4 Конфигурация (envconfig)

| Переменная | Default | Назначение |
|---|---|---|
| `KACHO_VPC_DB_HOST/PORT/USER/PASSWORD/NAME` | `localhost/5432/vpc/_/kacho_vpc` | Подключение к Postgres |
| `KACHO_VPC_DB_SSLMODE` | `disable` | `disable/require/verify-ca/verify-full` |
| `KACHO_VPC_DB_MAX_CONNS` | `0` (pgx default = `max(4, NumCPU)`) | Размер pgx pool'а. Прокидывается в DSN как `pool_max_conns` **только** для pgxpool — миграции используют DSN без него (иначе `database/sql` передает `pool_max_conns` серверу как unknown PG-параметр → fatal) |
| `KACHO_VPC_GRPC_PORT` | `9090` | Публичный gRPC |
| `KACHO_VPC_INTERNAL_PORT` | `9091` | Internal gRPC |
| `KACHO_VPC_IAM_GRPC_ADDR` | `iam.kacho.svc.cluster.local:9090` | Endpoint kacho-iam (`extapi.iam.endpoint`) |
| `KACHO_VPC_IAM_TLS` | `false` | TLS на канале к kacho-iam (`extapi.iam.tls.enable`) |
| `KACHO_VPC_DEFAULT_SG_INLINE` | `true` | `true` — `Network.doCreate` синхронно создает default SG. `false` — Network.Create НЕ создает SG (убирает 2 INSERT + 1 UPDATE из hot-path, +30-40% write-throughput; для load-тестов и deploy с внешним SG-reconciler'ом). При `false` newman-кейсы `*-LSG-CRUD-DEFAULT-SG` / `*-DEL-STATE-DEFAULT-SG` краснеют |
| `KACHO_VPC_AUTH_MODE` | `dev` | `dev / production / production-strict` |

`production-strict` требует `KACHO_VPC_IAM_TLS=true` и `DB_SSLMODE ∈
{require, verify-ca, verify-full}`. Любое отклонение — fatal exit.

---

## Часть III. Компонентный уровень (C3)

### 3.1 Слои Clean Architecture

```
                +----------------+
                |    handler     |  (transport — gRPC handlers + interceptors)
                +-------+--------+
                        |
                        v
+----------+    +-------+--------+    +----------+
|  repo    |<---+    service     +--->| clients  |
| (pgx)    |    | (use-cases)    |    | (gRPC)   |
+----------+    +-------+--------+    +----------+
                        |
                        v
                +-------+--------+
                |     domain     |  (entities — только stdlib + kacho-proto)
                +----------------+
```

Жесткое dependency rule: стрелки только сверху вниз и слева-направо.
`domain` и `service` не знают про `pgx`, `grpc`, `sqlc`. Port-интерфейсы
(`NetworkRepo`, `ProjectClient`, ...) объявляются per-use-case в `internal/apps/kacho/api/<resource>/iface.go`,
их реализуют `internal/repo/kacho/pg/*` и `internal/clients/*`. Wiring — в `cmd/vpc/main.go`,
больше никаких глобальных синглтонов.

### 3.2 Слой `domain/`

Чистые сущности без поведения. Один файл на ресурс. Минимальная зависимость —
`time`. Структуры flat (нет K8s envelope, generation, deletion_timestamp).

| Файл | Тип | Заметки |
|---|---|---|
| `network.go` | `Network` | `default_security_group_id` поле строкой |
| `subnet.go` | `Subnet` + `DhcpOptions` | CIDR-блоки строками, не `net.IPNet` |
| `address.go` | `Address`, `ExternalIpv4Spec`, `InternalIpv4Spec`, `AddressRequirements` | JSONB-формы для external/internal |
| `route_table.go` | `RouteTable` + `StaticRoute` | StaticRoute хранится как jsonb-массив |
| `security_group.go` | `SecurityGroup` + `SecurityGroupRule` | Rules embedded в jsonb |
| `gateway.go` | `Gateway` | `GatewayType` sentinel для oneof |
| `address_pool.go` | `AddressPool` + `AddressPoolKind` | Глобальный (без `project_id`) |
| `geography.go` | `Region`, `Zone` | Глобальные admin-ресурсы |
| `cloud_pool_selector.go` | `CloudPoolSelector` | Admin-controlled labels на Cloud |

### 3.3 Слой `service/`

Use-cases. Один файл на ресурс плюс общие модули.

| Файл | Содержимое |
|---|---|
| `<resource>/iface.go` | Per-use-case port-интерфейсы: `NetworkRepo`, `SubnetRepo`, `AddressRepo`, `RouteTableRepo`, `SecurityGroupRepo`, `GatewayRepo`, `ProjectClient`, `Pagination`, фильтры |
| `address_pool_ports.go` | Порты для IPAM (`AddressPoolRepo`, `AddressPoolBindingRepo`, `CloudPoolSelectorRepo`, `RegionRepo`, `ZoneRepo`) |
| `network.go` | `NetworkService` — Create/Update/Delete/Get/List + ListSubnets/ListSecurityGroups/ListRouteTables/ListOperations |
| `subnet.go` | `SubnetService` — выше + AddCidrBlocks/RemoveCidrBlocks/ListUsedAddresses |
| `address.go` | `AddressService` — выше + AllocateInternalIP/AllocateExternalIP + GetByValue/ListBySubnet |
| `route_table.go`, `security_group.go`, `gateway.go` | Аналогично, с domain-специфичными методами |
| `address_pool_service.go` | `AddressPoolService` — CRUD пулов + cascade resolve + bindings |
| `geography_service.go` | `RegionService`, `ZoneService` |
| `network_internal.go` | Внутренние операции над Network для admin-RPC |
| `errors.go` | Sentinel-ошибки: `ErrNotFound`, `ErrAlreadyExists`, `ErrInvalidArg`, `ErrFailedPrecondition`, `ErrInternal`, `ErrPoolNotResolved`, `ErrInvalidIPv4` |
| `maperr.go` | Единая функция трансляции sentinel-ошибок в gRPC-status |
| `validate.go` | Общие проверки (CIDR host-bits, IP в CIDR) |
| `cidr_util.go` | Хелперы для CIDR-арифметики |

### 3.4 Слой `repo/`

Pgx-адаптеры. Один файл на таблицу. Используют `pgxpool.Pool`,
`pgx.Rows`, `pgconn.PgError` для маппинга SQLSTATE.

| Файл | Что реализует |
|---|---|
| `kacho/pg/network.go` | `NetworkRepo` |
| `kacho/pg/subnet.go` | `SubnetRepo` (включая `SetCidrBlocks`, `SetZoneID`, `AddressesBySubnet`) |
| `kacho/pg/address.go` | `AddressRepo` (включая `GetByValue`, `SetIPSpec`) |
| `kacho/pg/route_table.go` | `RouteTableRepo` |
| `kacho/pg/security_group.go` | `SecurityGroupRepo` (включая `UpdateRules` с xmin-OCC, `UpdateRule`) |
| `kacho/pg/gateway.go` | `GatewayRepo` |
| `kacho/pg/address_pool.go` | `AddressPoolRepo` (включая cascade-SQL) |
| `kacho/pg/address_pool_binding.go` | `AddressPoolBindingRepo` (override/default привязки) |
| `kacho/pg/cloud_pool_selector.go` | `CloudPoolSelectorRepo` |
| `outbox.go` | `emitVPC` — обертка над `kacho-corelib/outbox.Emit` |
| `unique.go` | Распознавание SQLSTATE для unique/exclude/fk |
| `paging.go` | Кодирование/декодирование cursor-based page_token |
| `jsonb.go` | Хелперы для безопасной JSON-сериализации |

### 3.5 Transport-слой (gRPC handlers)

Тонкий transport: parse req → use-case → format resp. Public per-resource handler'ы
живут рядом со своими use-case'ами в `internal/apps/kacho/api/<resource>/handler.go`;
cross-cutting и internal-only transport — в `internal/handler/`.

Public per-resource handler'ы (`internal/apps/kacho/api/<resource>/handler.go`):

| Файл | Сервис |
|---|---|
| `network/handler.go` | `NetworkService` |
| `subnet/handler.go` | `SubnetService` |
| `address/handler.go` | `AddressService` |
| `routetable/handler.go` | `RouteTableService` |
| `securitygroup/handler.go` | `SecurityGroupService` |
| `gateway/handler.go` | `GatewayService` |
| `networkinterface/handler.go` | `NetworkInterfaceService` |
| `addresspool/handler.go` | `InternalAddressPoolService` (admin-only, :9091) |

Cross-cutting и internal transport (`internal/handler/`):

| Файл | Сервис / роль |
|---|---|
| `operation_handler.go` | `OperationService.Get` / `Cancel` |
| `internal_address_allocate_handler.go` | `InternalAddressService.AllocateInternalIP/IPv6/External` + referrer-tracking |
| `internal_network_handler.go` | `InternalNetworkService` (`GetNetwork` — internal-only `vrf_id`; `SetDefaultSecurityGroupId`) |
| `tenant_interceptor.go` | `TenantUnaryInterceptor`, `TenantStreamInterceptor` (tenant-context из gRPC metadata; `TenantCtx` / `AssertProjectOwnership` — в `internal/tenant`) |
| `internal_maperr.go` | Общий маппер ошибок internal-handlers без info-leak |

Конвертация `Operation` в proto — пакет `internal/apps/kacho/shared/pbconv`
(`OperationToProto`).

### 3.6 Слой `clients/`

`iam_client.go` (+ `project_cache.go`): реализует `ProjectClient` поверх
`grpc.ClientConn` к `kacho-iam`. Используется в worker'ах сервисов
для existence-check project и lookup `cloud_id`.

### 3.7 Слой `config/` и `migrations/`

- `config/config.go` — `Config` структура + `Load` через `kacho-corelib/config`.
  Разделение DSN: для pgxpool — DSN с `pool_max_conns` (если `DBMaxConns>0`); для
  миграций (`database/sql.Open("pgx")`) — DSN без него, иначе `pool_max_conns`
  уходит серверу как unknown PG-параметр → fatal.
- `internal/migrations/0001_initial.sql` — baseline-схема (все таблицы/индексы/
  constraints; embedded через `embed.FS`, объявлено в `migrations.go`).
  Partial UNIQUE `(project_id, name) WHERE name <> ''` для subnets/route_tables/
  security_groups/gateways/addresses объявлен здесь же.
- Инкрементные миграции `0002`+ — каждая отдельным файлом (см.
  [`architecture/05-database.md`](architecture/05-database.md)); примененную не редактируем.
- `migrations/` (в корне репо) — staging для `make sync-migrations`
  (только `0001_operations.sql` от corelib, источник истины не здесь).

### 3.8 `cmd/vpc/main.go` — composition root

Единственное место, где собираются все сервисы и регистрируются handler-ы.
Порядок:

1. Чтение `Config`.
2. Открытие `pgxpool.Pool`.
3. Создание `operations.Repo` (corelib).
4. Открытие gRPC-клиента к kacho-iam (`ProjectClient`).
5. Инстанцирование `*Repo` объектов.
6. Инстанцирование `*Service` объектов с проброшенными портами.
7. Инстанцирование двух `*grpc.Server` (публичный и internal) с
   `TenantUnaryInterceptor` / `TenantStreamInterceptor`.
8. Регистрация всех handler-ов на оба сервера (см. таблицы в §8).
9. Запуск listener-ов в отдельных горутинах.
10. Блокировка `Serve` на публичном listener'е.
11. На SIGTERM — `GracefulStop` обоих серверов + `operations.Wait(30s)`,
    блокировка на `shutdownDone` перед возвратом из `runServe`.

`cmd/` содержит composition root (`vpc/main.go`) и отдельный `migrator/`.
Admin-операции над IPAM (AddressPool/pool-selector/bindings) — REST на
cluster-internal listener api-gateway (`/vpc/v1/{addressPools,...}`) либо web-UI;
отдельного CLI нет. (Region/Zone admin — домен kacho-geo.)

---

## Часть IV. Поведенческие паттерны

### 4.1 Long-Running Operations (LRO)

Все мутации делятся на синхронную фазу (валидация, создание `Operation`)
и асинхронную фазу (worker-горутина: existence-checks, INSERT в БД,
outbox-emit, формирование response).

```
   client                handler               service               worker            repo
     |                     |                     |                      |               |
     |--CreateXxx--------->|                     |                      |               |
     |                     |--Create---------->|                      |               |
     |                     |                     |--sync validate------>|               |
     |                     |                     |--ops.Create(op)----->|               |
     |                     |<--Operation (done=false)                   |               |
     |<--Operation---------|                     |--go ops.Run(fn)----->|               |
     |                     |                                            |--Insert------>|
     |--Get(opId)--->...                                                |--emit outbox->|
     |                                                                  |--ops.SetDone|
     |--Get(opId)----------------------------------------------------->done=true        |
```

Контракты worker'а:

- Возвращает `(*anypb.Any, error)`. На успех — `Any` с proto-ресурсом
  (для Create/Update) или `&emptypb.Empty{}` (для Delete).
- На ошибку sentinel-формы (`ErrNotFound`, `ErrAlreadyExists`, ...) —
  результат записывается в `operation.error` с правильным gRPC-кодом.
- Worker не должен panic-ить; `operations.Run` ловит panics.
- При SIGTERM `operations.Wait` ждет завершения всех активных worker'ов
  (до 30 секунд), после чего процесс выходит.

### 4.2 Outbox + LISTEN/NOTIFY

Каждая successful мутация (внутри worker) пишет запись в `vpc_outbox`
в той же транзакции, что INSERT/UPDATE/DELETE ресурса:

| Колонка | Тип | Смысл |
|---|---|---|
| `sequence_no` | bigserial | Monotonic id события |
| `resource_kind` | text | `Network`, `Subnet`, ... |
| `resource_id` | text | id ресурса |
| `event_type` | text | `CREATED`, `UPDATED`, `DELETED` |
| `payload` | jsonb | Snapshot ресурса (через `domainToMap`) |
| `created_at` | timestamptz | now |
| `processed_at` | timestamptz | Зарезервировано на будущее |

Триггер `vpc_outbox_notify_trg` на каждый INSERT выполняет
`pg_notify('vpc_outbox', sequence_no::text)` — это in-cluster `LISTEN/NOTIFY`-канал
доменных событий. Публичного per-resource Watch RPC в Kachō нет: клиенты узнают об
изменениях через polling `List` / `OperationService.Get`.

**Транзакционная атомарность.** Repo-операции, эмитирующие outbox-событие,
обязаны:

1. Открыть транзакцию (`pool.BeginTx`).
2. Сделать INSERT/UPDATE/DELETE в ресурсной таблице.
3. Сделать `emitVPC(ctx, tx, kind, id, eventType, payload)` через ту же
   `pgx.Tx`.
4. COMMIT.

Если шаг 3 не в той же TX — событие может попасть в outbox без видимого
ресурса (или наоборот), что нарушит инвариант подписчиков. Pg_notify
шлется **после COMMIT** автоматически (триггер срабатывает на INSERT в
outbox, но notification отправляется в момент commit'а транзакции).
Подписчик гарантированно увидит ресурс в БД к моменту обработки события.

### 4.3 Наблюдение состояния — polling-модель (без Watch RPC)

Публичного per-resource Watch RPC в API Kachō нет — server-streaming-слежения за
состоянием ресурса сервис не предоставляет. Клиенты узнают об изменениях двумя
способами:

- **Результат мутации** — поллинг `OperationService.Get(operationId)` до `done=true`
  (см. §6 — Operations LRO worker).
- **Текущее состояние** — периодический `List<Resource>` (рекомендуемый интервал 2–5 c).

`vpc_outbox` остается транзакционным журналом доменных событий: каждая мутация в той
же TX пишет строку (`resource_kind/resource_id/event_type/payload`), триггер
`vpc_outbox_notify_trg` шлет `pg_notify('vpc_outbox', sequence_no)`. Это in-cluster
`LISTEN/NOTIFY`-канал; наружу как Watch RPC он не публикуется.

### 4.4 Inline IPAM allocation

Allocate выполняется внутри request-path service-слоя (раньше это делал
отдельный controller-процесс, теперь in-process):

- `AllocateInternalIP(addressID)` — двухфазный allocator поверх
  `addresses_internal_subnet_ip_uniq` UNIQUE: сначала sequential
  sweep usable IP в CIDR (до N штук), затем random retry до
  предельного числа попыток. Idempotent: если address уже имеет
  `internal_ipv4.address`, возвращает существующий с `already_allocated=true`.
- `AllocateExternalIP(addressID)` — cascade resolve пула (§7) + такой же
  двухфазный sweep по `cidr_blocks` пула + UNIQUE `addresses_external_pool_ip_uniq`.

Race-free на DB-уровне: при коллизии Postgres возвращает SQLSTATE 23505,
allocator перевыбирает IP. Идемпотентность дает безопасный retry на
сетевые сбои клиента.

### 4.5 Validation layering

Два уровня:

| Уровень | Когда | Что проверяет |
|---|---|---|
| Sync (до Operation) | В handler/service до возврата `Operation` | Required-поля, форматы (regex), CIDR host-bits, page_size, UpdateMask known-set, immutable-поля, deletion_protection |
| Async (внутри worker) | После `Operation` создана | Project existence через `ProjectClient` (`project_id` = id владельца-проекта), Network/Subnet existence, FK violations, EXCLUDE constraint (CIDR overlap), UNIQUE violations (duplicate name) |

Sync-ошибки → `Operation` не создается, клиент получает gRPC-error сразу.
Async-ошибки → `Operation` помечается failed с `google.rpc.Status` в поле
`error`. Клиент видит результат при `OperationService.Get`.

### 4.6 Error mapping

Единая точка трансляции — `service/maperr.go::mapRepoErr`:

| Sentinel error | gRPC код | Семантика |
|---|---|---|
| `ErrNotFound` | `NOT_FOUND` | Ресурс или зависимость отсутствует |
| `ErrAlreadyExists` | `ALREADY_EXISTS` | Дублирующее имя в project (UNIQUE 23505) |
| `ErrFailedPrecondition` | `FAILED_PRECONDITION` | CIDR overlap (EXCLUDE 23P01), сеть не пустая, deletion_protection |
| `ErrInvalidArg` | `INVALID_ARGUMENT` | Bad format, missing required field, mask violation |
| `ErrInternal` | `INTERNAL` | Default; текст не leak'ает pgx-detail |

Дополнительно `stripSentinel` снимает префикс sentinel из текста ошибки,
чтобы клиент видел канонический текст контракта. Internal handlers используют
`internalMapErr` — обобщенный маппер с защитой от info-leak (sentinel-only
тексты).

### 4.7 Pagination

Cursor-based, opaque base64 page_token. Сортировка ресурсов `ORDER BY
created_at ASC, id ASC`. `page_size`:

- `0` → DefaultPageSize = 50
- Максимум 1000
- Невалидный page_token → `INVALID_ARGUMENT`

Кодирование/декодирование в `repo/paging.go`.

### 4.8 UpdateMask discipline

Все Update RPC принимают `google.protobuf.FieldMask`. Поведение:

| Mask содержит | Реакция |
|---|---|
| Unknown поле | `INVALID_ARGUMENT` |
| Immutable поле | `INVALID_ARGUMENT` с текстом `"<field> is immutable after Xxx.Create"` |
| Mutable поле | Применяется, валидируется по правилам Create |
| Mask пустая | Full-object PATCH: применяются все mutable поля, immutable из тела silently игнорируются |

Immutable-поля по ресурсам:

| Ресурс | Immutable |
|---|---|
| Subnet | `v4_cidr_blocks`, `v6_cidr_blocks`, `network_id`, `zone_id` |
| Address | `external_ipv4_address_spec`, `internal_ipv4_address_spec`, `project_id` |
| Прочие | `project_id` |

### 4.9 AuthN/AuthZ scaffolding

В отсутствие IAM — interceptor читает metadata-headers:

| Header | Семантика |
|---|---|
| `x-kacho-project-id` (повторяемый) | Project, к которому caller имеет доступ |
| `x-kacho-admin: true` | Cluster-wide админ, минует project-check |

В context кладется `TenantCtx{ProjectIDs, Admin}`. Handler-ы
вызывают `AssertProjectOwnership(ctx, resource.ProjectID)` после `repo.Get`
и до возврата ресурса/мутации.

Anonymous (нет ни Admin, ни ProjectIDs) ведет себя по-разному в зависимости
от `KACHO_VPC_AUTH_MODE`:

| AuthMode | Поведение anonymous |
|---|---|
| `dev` | Полный доступ (backward-compat для тестов без AuthN) |
| `production` | `PERMISSION_DENIED` сразу в interceptor (fail-closed) |
| `production-strict` | То же + dополнительные проверки cross-service TLS и DB sslmode |

`requireAdmin=true` (для :9091 listener'а) — отвергает caller'а без
admin-flag. Точная семантика `assertAdminAccess`:

| Сценарий | Поведение |
|---|---|
| Anonymous (нет ни Admin, ни ProjectIDs) | Пропускается. В production-mode уже отвергнут вышестоящим guard-ом, в dev-mode это backward-compat для тестов без AuthN |
| Non-anonymous + Admin=true | Пропускается |
| Non-anonymous + Admin=false + method ∈ `/kacho.cloud.vpc.v1.Internal*` | `PERMISSION_DENIED "Permission denied"` |
| Non-anonymous + Admin=false + method не из Internal family | `NOT_FOUND "not found"` — camouflage, чтобы не светить структуру admin-listener'а |

Префикс-чек выполняется через `strings.HasPrefix`, а не `Contains` — это
защита от будущих сервисов со словом "Internal" в произвольной позиции
названия, которые могли бы случайно попасть в admin-listener.

---

## Часть V. Доменная модель

### 5.1 Публичные ресурсы (7)

Все project-scoped, имеют общий минимум полей: `id`, `project_id`, `created_at`,
`name`, `description`, `labels`.

| Ресурс | ID prefix | Доп. поля | Особенности |
|---|---|---|---|
| Network | `net` | `default_security_group_id`, `route_distinguisher`, `vrf_id` (internal-only) | default SG создается inline в `doCreate` (опционально — `KACHO_VPC_DEFAULT_SG_INLINE`, default `true`); `vrf_id` — internal-only инфра-идентификатор, на публичной поверхности нет |
| Subnet | `sub` | `network_id`, `zone_id`, `v4_cidr_blocks[]`, `v6_cidr_blocks`, `route_table_id`, `dhcp_options` | EXCLUDE на `(network_id, v4_cidr_primary)` (и v6); **`v4_cidr_blocks` опционально на Create** (CIDR-less подсеть легальна); `:add/:remove-cidr-blocks` принимают и `v6_cidr_blocks`; `zone_id` — id-строка домена geo (без FK) |
| Address | `adr` | `addr_type`, `ip_version`, `reserved`, `used`, `used_by`, `deletion_protection`, `external_ipv4` (jsonb), `internal_ipv4` (jsonb), `internal_ipv6` (jsonb) | Generated `internal_subnet_id` (из `internal_ipv4` ИЛИ `internal_ipv6`) → FK `addresses_internal_subnet_fkey ON DELETE RESTRICT`; `internal_ipv6_address_spec` + `InternalAddressService.AllocateInternalIPv6`; `Delete` used-адреса (referrer=NIC) → `FailedPrecondition` |
| NetworkInterface | `nic` | `subnet_id` (FK RESTRICT), `mac_address`, `v4_address_ids[]`/`v6_address_ids[]` (ссылки на Address по id), `security_group_ids[]`, `used_by` (Reference — Attach/Detach), `status` enum | first-class самостоятельный сетевой интерфейс (отдельный от Instance); может быть создан без адресов; один Address ≤ на одном NIC (referrer-rows `address_references`, `referrer_type="network_interface"`); проекция чисто control-plane (lean) — инфра-полей у kacho-vpc нет |
| RouteTable | `rtb` | `network_id`, `static_routes` (jsonb-массив) | Static-routes embedded |
| SecurityGroup | `sgr` | `network_id` (**NULLABLE**), `status`, `default_for_network`, `rules` (jsonb) | Rules embedded; xmin-OCC; **`network_id` опционально на Create** (project-level / network-less SG); `List?filter=network_id="<id>"` |
| Gateway | `gtw` | `gateway_type` | Project-level, не привязан к Network |

**Замечание про префиксы.** У каждого ресурса свой 3-char префикс
(`net`/`sub`/`adr`/`nic`/`rtb`/`sgr`/`gtw`/`apl`). Operation у VPC несет
**отдельный** префикс `PrefixOperationVPC = "enp"` (декаплен от ресурсных префиксов):
api-gateway смотрит на первые 3 символа Operation.id и направляет
`OperationService.Get(opId)` в нужный backend.

### 5.2 IPAM-ресурсы (admin-only)

| Ресурс | ID prefix | Глобальный | Заметки |
|---|---|---|---|
| AddressPool | `apl` (3-char, обязательный формат `corelib/ids`) | да | Не имеет `project_id`; `kind` enum; `zone_id` — id-строка домена compute без FK; `selector_labels` jsonb |
| CloudPoolSelector | PK = `cloud_id` | n/a | Не имеет своего id, ключ — Cloud |

> Region/Zone — **не в kacho-vpc**; канонический владелец — leaf-домен `kacho-geo`.
> `subnet.zone_id` / `address_pool.zone_id` хранятся как `TEXT`-id без FK, валидируются на
> request-path через `geo.v1.ZoneService.Get`.

### 5.3 Binding-таблицы (3)

| Таблица | PK | Семантика |
|---|---|---|
| `address_pool_address_override` | `address_id` | Привязка конкретного Address к конкретному пулу (explicit per-address) |
| `address_pool_network_default` | `network_id` | Default-pool для Network (explicit per-network) |
| `cloud_pool_selector` | `cloud_id` | Admin-labels Cloud (legacy-таблица; в текущем cascade не используется) |

### 5.4 Operations + Outbox

| Таблица | Назначение | PK |
|---|---|---|
| `operations` | Long-running operations (синхронизирована с corelib) | `id` |
| `vpc_outbox` | Транзакционный журнал доменных событий (in-cluster `LISTEN/NOTIFY`; Watch RPC не публикуется) | `sequence_no` |
| `vpc_watch_cursors` | Vestigial-таблица из baseline-схемы; кодом не используется (Watch RPC удален) | `subscriber_id` |

### 5.5 Связи между ресурсами (FK contract)

```
   Network (1) ──+── (N) Subnet ──+── (N) Address[internal v4/v6]
                 |                 └── (N) NetworkInterface  (subnet_id, RESTRICT)
                 |
                 +── (N) RouteTable
                 |
                 +── (N) SecurityGroup ── (N) SecurityGroupRule (embedded)
                 |
                 └── default_security_group_id ─── soft ref (not FK)

   Address[external] ── project-level, без Subnet
   NetworkInterface  ── принадлежит Subnet; ссылается на Address по id (v4_address_ids/v6_address_ids),
                        security_group_ids[]; used_by — Reference (кто использует NIC)
   Gateway           ── project-level, без Network
```

**Dependency / delete-blocking chain:** NIC → Address → Subnet → Network — все RESTRICT, удаление снизу вверх:
- `Address.Delete` used-адреса (referrer = NIC) → `FailedPrecondition "address ... is in use by network interface ...; detach it before deleting the address"`. Освободить — detach от NIC / удаление NIC.
- `Subnet.Delete` — sync-precheck: есть внутренний Address (v4 ИЛИ v6 — `AddressesBySubnet` смотрит и `internal_ipv4`, и `internal_ipv6`) → `FailedPrecondition "Subnet has allocated internal addresses"`; есть NIC → `FailedPrecondition "subnet ... has N network interface(s) (...); delete them first"`. DB-backstops: `addresses_internal_subnet_fkey` (на generated-колонке `addresses.internal_subnet_id`) + `network_interfaces_subnet_id_fkey ON DELETE RESTRICT`.
- `Network.Delete` непустой (subnets / route tables / non-default SG) → `FailedPrecondition "Network ... is not empty"`; default SG авто-удаляется Delete-worker'ом.

Реальные FK constraint-ы в схеме:

| Источник | Колонка | Цель | ON DELETE |
|---|---|---|---|
| `subnets` | `network_id` | `networks(id)` | NO ACTION (default) |
| `route_tables` | `network_id` | `networks(id)` | NO ACTION (default) |
| `security_groups` | `network_id` (**NULLABLE**) | `networks(id)` | RESTRICT |
| `addresses` | `internal_subnet_id` (generated, из `internal_ipv4` ИЛИ `internal_ipv6`) | `subnets(id)` | RESTRICT |
| `network_interfaces` | `subnet_id` | `subnets(id)` | RESTRICT |
| `address_references` | `address_id` | `addresses(id)` | CASCADE |
| `address_pool_address_override` | `address_id` | `addresses(id)` | CASCADE |
| `address_pool_address_override` | `pool_id` | `address_pools(id)` | RESTRICT |
| `address_pool_network_default` | `network_id` | `networks(id)` | CASCADE |
| `address_pool_network_default` | `pool_id` | `address_pools(id)` | RESTRICT |

(`address_pools.zone_id` — `TEXT`-id домена geo, без FK; Geography — leaf-домен kacho-geo.)

Замечания:

- `Network → default_security_group_id` и кросс-ссылки NIC →
  `security_group_ids[]`/`v4_address_ids[]`/`v6_address_ids[]` —
  enforced на сервис-уровне (existence-check / referrer-tracking в worker), а **не** FK на
  БД-уровне. Removal default-SG ставит пустую строку в поле (а не NULL через SET NULL).
- NO ACTION в Postgres эквивалентен RESTRICT для DELETE по умолчанию —
  оба отвергают удаление родителя при наличии детей.
- CASCADE на binding/referrer-таблицах: удаление address/network автоматически
  убирает их override/default/referrer-row; pool не удалится при наличии bindings (RESTRICT).

---

## Часть VI. БД-схема

### 6.1 Таблицы

Source of truth — `internal/migrations/*.sql`: `0001_initial.sql` (baseline-схема)
+ инкрементные `0002`+ (см. [`architecture/05-database.md`](architecture/05-database.md)
для полного списка). Ключевые таблицы:

| Таблица | Колонки (ключевые) |
|---|---|
| `operations` | `id text PK`, `description`, `created_at`, `created_by`, `done`, `metadata_type`, `metadata_data bytea`, `resource_id`, `response_type`, `response_data bytea`, `error_*` |
| `networks` | `id text PK`, `project_id`, `created_at`, `name`, `description`, `labels jsonb`, `default_security_group_id`, `route_distinguisher`, `vrf_id bigint` (internal-only) |
| `subnets` | `id`, `project_id`, `created_at`, `name`, `description`, `labels`, `network_id`, `zone_id text` (без FK — geography→geo), `v4_cidr_blocks text[] DEFAULT '{}'` (опционально на Create), `v6_cidr_blocks jsonb`, `route_table_id`, `dhcp_options jsonb`, `v4_cidr_primary cidr GENERATED`, `v6_cidr_primary cidr GENERATED` |
| `addresses` | `id`, `project_id`, `created_at`, `name`, `description`, `labels`, `addr_type smallint`, `ip_version smallint`, `reserved`, `used`, `used_by_type/id/name`, `deletion_protection`, `external_ipv4 jsonb`, `internal_ipv4 jsonb`, `internal_ipv6 jsonb`, `internal_subnet_id text GENERATED` (из `internal_ipv4` ИЛИ `internal_ipv6`) |
| `network_interfaces` | `id text PK` (`nic…`), `project_id`, `created_at`, `name`, `labels`, `subnet_id text NOT NULL FK→subnets ON DELETE RESTRICT`, `mac_address text`, `v4_address_ids text[]`, `v6_address_ids text[]`, `security_group_ids text[]`, `used_by_type/id/name text`, `status smallint` |
| `address_references` | `address_id text PK FK→addresses ON DELETE CASCADE`, `referrer_type text` (`compute_instance` \| `network_interface`), `referrer_id`, `referrer_name`, `attached_at` |
| `route_tables` | `id`, `project_id`, `created_at`, `name`, `description`, `labels`, `network_id`, `static_routes jsonb` |
| `security_groups` | `id`, `project_id`, `network_id text` (**NULLABLE**), `created_at`, `name`, `description`, `labels`, `status`, `default_for_network`, `rules jsonb` |
| `gateways` | `id`, `project_id`, `created_at`, `name`, `description`, `labels`, `gateway_type` |
| `address_pools` | `id`, `name`, `description`, `labels`, `cidr_blocks text[]`, `kind smallint`, `is_default`, `zone_id text` (без FK — geography→compute), `selector_labels jsonb`, `selector_priority` |
| `address_pool_address_override` | `address_id PK`, `pool_id`, `bound_at` |
| `address_pool_network_default` | `network_id PK`, `pool_id`, `bound_at` |
| `cloud_pool_selector` | `cloud_id PK`, `selector jsonb`, `set_at`, `set_by` |
| `vpc_outbox` | `sequence_no bigserial PK`, `resource_kind`, `resource_id`, `event_type`, `payload jsonb`, `created_at`, `processed_at` |
| `vpc_watch_cursors` | `subscriber_id PK`, `last_sequence_no`, `updated_at` |

(`regions`/`zones` — таблиц в kacho-vpc нет; Geography — leaf-домен kacho-geo, ссылка по `zone_id` без FK.)

### 6.2 Ключевые constraints

| Объект | Тип | Назначение |
|---|---|---|
| `subnets_no_overlap_v4` | `EXCLUDE USING gist (network_id WITH =, v4_cidr_primary inet_ops WITH &&)` | Race-free защита от CIDR-overlap внутри одной Network (только primary CIDR) |
| `subnets_no_overlap_v6` | Аналогично для v6 | То же для IPv6 |
| `addresses_external_ip_uniq` | UNIQUE на `external_ipv4 ->> 'address'` (partial) | Глобальная уникальность external IP |
| `addresses_external_pool_ip_uniq` | UNIQUE на `(external_ipv4 ->> 'address_pool_id', external_ipv4 ->> 'address')` (partial) | Race-free allocator поверх (pool, ip) |
| `addresses_internal_subnet_ip_uniq` | UNIQUE на `(internal_ipv4 ->> 'subnet_id', internal_ipv4 ->> 'address')` (partial) | То же для internal IPv4 в Subnet |
| `addresses_internal_subnet_ipv6_uniq` | UNIQUE на `((internal_ipv6 ->> 'subnet_id'), (internal_ipv6 ->> 'address'))` (partial) | То же для internal IPv6; conflict-target для `AllocateInternalIPv6` |
| `addresses_internal_subnet_fkey` | FK `(internal_subnet_id) → subnets(id) ON DELETE RESTRICT` (generated col покрывает v4+v6) | v4/v6-internal-адрес блокирует удаление своей подсети |
| `network_interfaces_subnet_id_fkey` | FK `(subnet_id) → subnets(id) ON DELETE RESTRICT` | NIC блокирует удаление своей подсети |
| `networks_project_id_name_key` | UNIQUE `(project_id, name)` | Имя сети уникально в project |
| `{subnets,route_tables,security_groups,gateways,addresses}_project_id_name_key` | UNIQUE `(project_id, name)` WHERE `name <> ''` | Имя уникально в project для остальных 5 ресурсов; пустой `name` допускает несколько |
| `address_pools_zone_kind_default_uniq` | UNIQUE `(COALESCE(zone_id,''), kind)` WHERE `is_default=true` | Не более одного дефолтного пула на `(zone, kind)` |

### 6.3 Индексы (helper)

- `*_created_at_idx`, `*_project_idx` — для пагинации и WHERE-фильтра.
- `*_network_idx`, `addresses_internal_subnet_idx` — для cascade-фильтров.
- `address_pools_selector_labels_gin` — GIN-индекс с `jsonb_path_ops` для
  `@>`-запроса в label-cascade.
- `cloud_pool_selector_gin` — то же для CloudPoolSelector.
- `vpc_outbox_seq_idx`, `vpc_outbox_kind_idx` — для выборок по `sequence_no` / `resource_kind` в outbox-журнале.
- `operations_resource_idx` — для `ListOperations` per-resource.

### 6.4 Triggers и функции

| Объект | Назначение |
|---|---|
| `vpc_outbox_notify` PL/pgSQL | Шлет `pg_notify('vpc_outbox', NEW.sequence_no::text)` |
| `vpc_outbox_notify_trg` AFTER INSERT | Вызывает функцию выше для каждого нового события |

Других триггеров нет — атомарность outbox обеспечивается одной TX на стороне приложения.

### 6.5 SQLSTATE → sentinel mapping

| SQLSTATE | Имя | Источник | Sentinel | gRPC код |
|---|---|---|---|---|
| `23505` | `unique_violation` | UNIQUE index (duplicate name, duplicate external IP) | `ErrAlreadyExists` | `ALREADY_EXISTS` |
| `23P01` | `exclusion_violation` | EXCLUDE constraint (CIDR overlap в subnets) | `ErrFailedPrecondition` | `FAILED_PRECONDITION` |
| `23503` | `foreign_key_violation` | FK на родителя или RESTRICT при удалении | `ErrFailedPrecondition` или `ErrNotFound` (зависит от контекста) | `FAILED_PRECONDITION` / `NOT_FOUND` |
| `23502` | `not_null_violation` | NOT NULL поле без значения | `ErrInvalidArg` | `INVALID_ARGUMENT` |
| `pgx ErrNoRows` | — | Get не нашел строку | `ErrNotFound` | `NOT_FOUND` |
| прочие | — | Любая другая ошибка БД | `ErrInternal` (text не leak'ается) | `INTERNAL` |

Распознавание SQLSTATE в `repo/unique.go` через `pgconn.PgError.Code`.

### 6.6 Generated columns

| Таблица | Колонка | Выражение |
|---|---|---|
| `subnets` | `v4_cidr_primary` | Первый элемент `v4_cidr_blocks`, приведенный к `cidr` (STORED) |
| `subnets` | `v6_cidr_primary` | То же для v6 |
| `addresses` | `internal_subnet_id` | `internal_ipv4 ->> 'subnet_id'` **ИЛИ** `internal_ipv6 ->> 'subnet_id'` если непусто (STORED) — покрывает v4 и v6, и тот, и другой internal-адрес блокирует свою подсеть через FK `addresses_internal_subnet_fkey` |

Generated STORED-колонки нужны индексам и EXCLUDE-constraint'у, которые
не умеют работать с выражениями `(jsonb_field)::cidr` напрямую.

---

## Часть VII. IPAM в деталях

### 7.1 Cascade resolve (3 шага)

При `AllocateExternalIP(address_id)` сервис ищет первый подходящий пул
по следующему порядку (family-aware: на каждом шаге pool
пропускается, если его CIDR-список для запрошенного family пуст):

1. **network default** — `address_pool_network_default[address.network_id]` → pool.
2. **zone default** — `address_pools` где `is_default=true`, `zone_id =
   address.zone_id`, `kind` совпадает.
3. **global default** — `address_pools` где `is_default=true`, `zone_id IS NULL`, `kind` совпадает.

Если ни один шаг не дал результата → `ErrPoolNotResolved` → `FAILED_PRECONDITION`.

### 7.2 Match-семантика (containment, не subset)

`cloud.selector ⊆ pool.selector_labels` (pool описывает **whitelist
разрешенных labels** для Cloud). Семантика inverse относительно
k8s NodeSelector — safe-by-default: неучтенная комбинация labels попадает
в default-pool, а не в специальный через subset-trick.

| Pool `selector_labels` | Cloud selector | Match? |
|---|---|---|
| `{tier=premium}` | `{}` | да |
| `{tier=premium}` | `{tier=premium}` | да |
| `{tier=premium}` | `{tier=premium, customer=acme}` | нет (`customer` не в pool) |
| `{tier=premium, customer=acme}` | `{tier=premium}` | да |

### 7.3 Двухфазный allocator

Параметры (`internal/apps/kacho/api/address/create.go::const`):

| Константа | Значение | Смысл |
|---|---|---|
| `allocateRandomPhase` | 8 | Сколько попыток сделать random-pick до переключения на sweep |
| `allocateMaxAttempts` | 32 | Общий лимит попыток (random + sweep) |

**Фаза 1 — random pick (cheap path).** До `allocateRandomPhase` попыток
`pickRandomIPv4(cidr)` выбирает случайный host-IP из usable-диапазона;
делается `UPDATE addresses SET external_ipv4 = {ip, pool_id}`; при SQLSTATE
23505 — следующий random. Для low/medium occupancy сходится за 1–2 попытки.

**Фаза 2 — deterministic sweep с tried-set.** Если random за 8 попыток не
сошелся (high occupancy: ≥95% занято), allocator переключается на
`usableIPv4Sweep(cidr, maxN)` — итерация подряд по host-IP, исключая
network/broadcast и уже tried-IP из фазы 1. Гарантированное закрытие за
конечное число попыток.

**Iterate по всем CIDR-блокам.** В `AllocateInternalIP` ходит по всем
`Subnet.V4CidrBlocks`, не только по `[0]` — критично для подсетей, которые
расширили через `AddCidrBlocks`. В `AllocateExternalIP` ходит по всем
`Pool.CIDRBlocks`.

Для типичного /24 pool'а с малой утилизацией выделение завершается за 1
SQL-запрос; на /20 c >90% утилизации — в пределах 32 попыток. Если ни
один candidate не подошел — `ResourceExhausted`.

### 7.4 Идемпотентность

`AllocateInternalIP` и `AllocateExternalIP`:

- Lookup address.
- Если `external_ipv4.address` / `internal_ipv4.address` уже заполнен —
  возвращается existing с флагом `already_allocated=true`. Никаких
  повторных SQL-запросов.

Это дает клиенту безопасный retry на сетевые таймауты.

### 7.5 Cardinality / ambiguity

Если несколько пулов имеют одинаковый `(zone_id, kind, selector_labels,
selector_priority)` — резолв возвращает первый по physical order Postgres
(undefined). Admin обязан различать пулы через `selector_priority`. Для
обнаружения проблем есть утилитарный RPC `Check` (возвращает warnings)
и `Explain` (показывает, какой пул выбран для конкретного Network).

---

## Часть VIII. API-поверхность

### 8.1 Публичные RPC (:9090)

| Service | RPC | Тип | Описание |
|---|---|---|---|
| `NetworkService` | Get, List, ListSubnets, ListSecurityGroups, ListRouteTables, ListOperations | sync | Чтения |
| `NetworkService` | Create, Update, Delete | async | Мутации, возвращают `Operation` |
| `SubnetService` | Get, List, ListOperations, ListUsedAddresses | sync | Чтения |
| `SubnetService` | Create, Update, Delete, AddCidrBlocks, RemoveCidrBlocks | async | Мутации |
| `AddressService` | Get, List (фильтр `subnet_id` матчит `internal_ipv4`/`internal_ipv6`), GetByValue, ListBySubnet, ListOperations (переживает удаление) | sync | Чтения |
| `AddressService` | Create (+ `internal_ipv6_address_spec`), Update, Delete (used-адрес у NIC → `FailedPrecondition`) | async | Мутации |
| `SubnetService` (доп.) | AddCidrBlocks / RemoveCidrBlocks — теперь принимают и `v6_cidr_blocks`; `v4_cidr_blocks` опционально на Create; `UpdateSubnet` получил `v6_cidr_blocks` (soft-immutable) | async | — |
| `NetworkInterfaceService` | Get, List, ListOperations (переживает удаление) | sync | Чтения |
| `NetworkInterfaceService` | Create, Update, Delete | async | Мутации; Create — `subnet_id` обязателен, адреса/SG опциональны; `used_by` — денормализованное зеркало |
| `SecurityGroupService` (доп.) | Create — `network_id` опционален (project-level SG); `List?filter=network_id="<id>"` | — | — |
| `RouteTableService` | Get, List, ListOperations | sync | Чтения |
| `RouteTableService` | Create, Update, Delete, AddRoutes, RemoveRoutes, UpdateRoute | async | Мутации |
| `SecurityGroupService` | Get, List, ListOperations | sync | Чтения |
| `SecurityGroupService` | Create, Update, Delete, UpdateRules, UpdateRule | async | Мутации; xmin-OCC для UpdateRules |
| `GatewayService` | Get, List, ListOperations | sync | Чтения |
| `GatewayService` | Create, Update, Delete | async | Мутации |
| `OperationService` | Get | sync | Pollable status |

### 8.2 Internal RPC (:9091)

| Service | RPC | Назначение |
|---|---|---|
| `InternalAddressService` | AllocateInternalIP, **AllocateInternalIPv6**, AllocateExternalIP, {Set,Clear,Get}AddressReference | IPAM + referrer-tracking; вызывается in-process из `AddressService.doCreate` и kacho-compute (referrer'ы: `compute_instance`, `network_interface`) |
| `InternalAddressPoolService` | CRUD пулов + BindAsNetworkDefault/UnbindNetworkDefault + ListAddresses/GetUtilization | Управление IPAM (cluster-internal listener) |
| `InternalNetworkService` | GetNetwork (internal-only `vrf_id`), SetDefaultSecurityGroupId | read инфра-чувствительного поля + admin-only computed-field setter (default SG) |
| ~~`InternalRegionService` / `InternalZoneService`~~ | — | Geography (Region/Zone) живет в leaf-домене `kacho-geo`; в kacho-vpc этих сервисов нет |

### 8.3 REST mapping (через api-gateway)

Публичные RPC проброшены api-gateway grpc-gateway'ем по схеме
`POST /vpc/v1/<resource>/...`. Internal RPC доступны только через
cluster-internal listener api-gateway и не публикуются на TLS-endpoint.

### 8.4 ID format

ID получается через `kacho-corelib/ids.NewID(prefix)` (см. таблицу в §5.1).
Колонки — `TEXT`. Каждый id-берущий RPC первым стейтментом вызывает
`corevalidate.ResourceID(resourceType, ids.PrefixXxx, id)`: нераспознанный id
(нет известного 3-char prefix `net/sub/adr/rtb/sgr/gtw/nic/apl/enp`) → sync `INVALID_ARGUMENT
"invalid <res> id '<X>'"`; well-formed-но-несуществующий
(известный prefix) → `NOT_FOUND` через `repo.Get`. Семантика family-agnostic
(валидный-по-форме id чужого ресурса проходит prefix-check → `repo.Get` → `NOT_FOUND`).

---

## Часть IX. Sequence-диаграммы

### 9.1 Create Network с inline default-SG

```
client      api-gw    vpc.handler    vpc.service    projectClient    iam    networkRepo    sgRepo    outbox
  |           |           |              |               |          |         |            |          |
  |--POST----+|           |              |               |          |         |            |          |
  |           |--gRPC---->|              |               |          |         |            |          |
  |           |           |--Create--->|               |          |         |            |          |
  |           |           |              |--validate---->|          |         |            |          |
  |           |           |              |--ops.New----->|          |         |            |          |
  |           |           |              |--ops.Create-->|          |         |            |          |
  |           |           |<--Operation(done=false)      |          |         |            |          |
  |<--Operation                                          |          |         |            |          |
  |                                                     async worker:                                  |
  |                       go fn-->     |               |          |         |            |          |
  |                                      |--Exists---->|--Get---->|         |            |          |
  |                                      |<--true                              |            |          |
  |                                      |--Insert(network)----------------->BEGIN          |          |
  |                                      |--Insert(default SG)------------------>           |          |
  |                                      |--Network UPDATE default_sg_id---->                          |
  |                                      |--emit outbox Network.CREATED-------------------->INSERT     |
  |                                      |--emit outbox SecurityGroup.CREATED-------------->INSERT     |
  |                                      |                                                COMMIT       |
  |                                      |--ops.SetDone------------------------------------>         |
  |                                                                                                    |
  |--Get(opId)--+                                                                                      |
  |             |--gRPC---> ops.Get -->done=true, response=Network                                     |
  |<--Network---+                                                                                      |
```

> При `KACHO_VPC_DEFAULT_SG_INLINE=false` шаги `Insert(default SG)` /
> `Network UPDATE default_sg_id` / `emit SecurityGroup.CREATED` пропускаются —
> `Network.default_security_group_id` остается пустым (создание default SG
> делегируется внешнему reconciler'у). Default — `true`.

### 9.2 Allocate External IP (cascade + двухфазный allocator)

```
caller         addrSvc       poolSvc       addrPoolRepo    bindRepo    addrRepo    pg
  |               |             |               |             |           |         |
  |--Allocate--->|              |               |             |           |         |
  |               |--Get(addr)------------------------------------->      |         |
  |               |<--Address, external_ipv4=nil                          |         |
  |               |--ResolvePool-->                                       |         |
  |               |               |--bindings(addr)----------+|           |         |
  |               |               |<--nil                    +|           |         |
  |               |               |--bindings(network)--------+           |         |
  |               |               |<--nil                                 |         |
  |               |               |--cloud selector (by ProjectClient.GetCloudIDFromProject) |
  |               |               |--label-match SQL------>|              |         |
  |               |               |<--pool / nil           |              |         |
  |               |               |--zone default--->|     |              |         |
  |               |               |<--pool / nil     |     |              |         |
  |               |               |--global default->|     |              |         |
  |               |<--pool                                                |         |
  |               |--phase 1 random pick (≤8 attempts)-->|                |         |
  |               |   loop attempt < 8:                  |                |         |
  |               |   |  ip = pickRandomIPv4(cidr)       |                |         |
  |               |   |  UPDATE addresses SET external_ipv4 = {ip, pool_id} ------>23505?
  |               |   |  remember tried; if success → break                          |
  |               |   end                                                            |
  |               |--phase 2 sweep (если phase 1 не сошлась)                         |
  |               |   loop по cidr_blocks pool'а:                                    |
  |               |   |  for ip in usableIPv4Sweep(cidr, maxN):                      |
  |               |   |    if ip ∈ tried: continue                                   |
  |               |   |    UPDATE addresses SET external_ipv4 = {ip, pool_id}-->23505?
  |               |   |    success → break                                           |
  |               |   end                                                            |
  |               |--общий лимит: 32 попытки → иначе ResourceExhausted               |
  |<--AllocResult{ip, pool_id, already_allocated=false}                              |
```

### 9.3 Наблюдение состояния (polling)

Server-streaming Watch RPC в API Kachō нет. Клиент узнает о результате мутации
поллингом `OperationService.Get(operationId)` до `done=true`, а текущее состояние —
периодическим `List<Resource>`:

```
client                      kacho-vpc                 pg
  |                            |                       |
  |--Create/Update/Delete----->|--INSERT ресурс + vpc_outbox (одна TX)-->|
  |<--Operation{done=false}----|                       |
  |  loop до done=true:        |                       |
  |--OperationService.Get----->|--SELECT operations--->|
  |<--Operation{done?, result}-|                       |
  |  периодический re-List:    |                       |
  |--List<Resource>----------->|--SELECT ресурсы------>|
  |<--текущее состояние--------|                       |
```

`vpc_outbox` остается транзакционным журналом доменных событий (`pg_notify`
in-cluster); наружу как Watch RPC он не публикуется.

### 9.4 Subnet AddCidrBlocks (с EXCLUDE constraint защитой)

```
client       handler        service             subnetRepo      pg
  |            |              |                    |              |
  |--Add------>|              |                    |              |
  |            |--Add------>|                    |              |
  |            |              |--validate-disjoint-|              |
  |            |              |--Get(subnet)--------------------> |
  |            |              |<--Subnet                          |
  |            |              |--ops.New + ops.Create             |
  |            |<--Operation--|                                    |
  |<--Operation                                                    |
  |                            async worker:                       |
  |                            |--SetCidrBlocks(v4)-----------> UPDATE
  |                            |                                v4_cidr_primary recompute (GENERATED)
  |                            |                                EXCLUDE check: any other subnet with overlap?
  |                            |                                   23P01? --> error
  |                            |<--*Subnet или err                  |
  |                            |--emit outbox Subnet.UPDATED------> |
  |                            |--ops.SetDone(response=Subnet)      |
```

### 9.5 Graceful shutdown

```
                    main goroutine        shutdown goroutine        register-drainer    LRO worker
                          |                       |                      |                  |
SIGTERM ------>           |                       |                      |                  |
                          |--ctx cancel--->       |                      |                  |
                          |                       |--<-ctx.Done        |                  |
                          |                       |--internalSrv.GracefulStop                |
                          |                       |                      ⟵-- ctx canceled  |
                          |                       |--grpcSrv.GracefulStop                    |
                          |                       |  (отвергает новые RPC, ждет активные)    |
                          |                       |--operations.Wait(30s)                    |
                          |                       |                                          ⟵ ждем
                          |                       |                                          worker возвращает
                          |                       |--close(shutdownDone)                     |
                          |--<-shutdownDone-      |                                          |
                          |--exit                                                            |
```

---

## Часть X. Операционные аспекты

### 10.1 Команды Makefile

| Команда | Назначение |
|---|---|
| `make build` | Сборка `bin/kacho-vpc` |
| `make test-short` | Unit-тесты без testcontainers |
| `make test` | Unit + integration (нужен Docker) |
| `make sync-migrations` | Копирует `0001_operations.sql` из corelib в staging |

### 10.2 Запуск миграций

`bin/kacho-vpc migrate up` — goose, source FS — embed.FS `internal/migrations`
(`0001_initial.sql` — baseline-схема + инкрементные `0002`+). При первом запуске
goose создает `goose_db_version` автоматически. Миграции используют DSN
без `pool_max_conns` (иначе `database/sql` шлет его серверу как unknown PG-параметр → fatal).

### 10.3 Health probe

Не реализован отдельный endpoint. Liveness — pgx ping в `coredb.NewPool`
при старте; readiness — успешный bind на gRPC-порту. В продовом deploy
используется `grpcurl` health check на reflection.

### 10.4 Observability

- Логи — `kacho-corelib/observability.NewSlogger` (slog в JSON или text).
- Метрики — не вынесены (GitHub Issue — observability gap); ожидается prometheus exporter на отдельном порту.
- Trace — не реализован.

### 10.5 Деплой через Helm

`deploy/` содержит свой Chart.yaml + templates + values.yaml. Используется
umbrella-чартом `kacho-deploy` для dev-стенда (kind + Postgres + все сервисы).

---

## Часть XI. Безопасность

### 11.1 AuthN/AuthZ уровни

| Уровень | Защита |
|---|---|
| Транспорт (cross-service) | TLS на gRPC к kacho-iam (`KACHO_VPC_IAM_TLS`) |
| Транспорт (cross-service DB) | sslmode для pgx DSN |
| AuthN | Сейчас не реализован — IAM scaffolding через metadata headers, future — JWT |
| AuthZ (project ownership) | `AssertProjectOwnership(ctx, project_id)` во всех публичных handler-ах |
| AuthZ (admin operations) | `requireAdmin=true` interceptor на :9091 |
| Сетевая защита :9091 | k8s NetworkPolicy (allowlist namespaces) |
| Info-leak guard | `mapRepoErr`/`internalMapErr` не передают raw err.Error в gRPC-text |

### 11.2 Fail-closed mode

`KACHO_VPC_AUTH_MODE=production` (или `production-strict`) делает interceptor
fail-closed: anonymous caller (нет Admin и нет ProjectIDs) → `PERMISSION_DENIED`
сразу в interceptor, до handler-а. Это защищает от misconfigured deploy без
IAM sidecar — иначе anonymous = root.

### 11.3 production-strict дополнительно

- Требует `KACHO_VPC_IAM_TLS=true`.
- Требует `DB_SSLMODE ∈ {require, verify-ca, verify-full}` (не `prefer/allow`,
  потому что они допускают TLS-fallback к plaintext под MITM).
- Любое отклонение → процесс не стартует.

### 11.4 Известные ограничения / пробелы

- AuthN не реализован — service полагается на сетевой периметр + IAM
  sidecar (future).
- `OperationService.Get` не делает project-AuthZ (требует data-model change —
  добавить `project_id` в `operations` или join через `metadata.resource_id`).
- mTLS на :9091 — пока опциональный (TLS на listener'е возможен, но
  NetworkPolicy + admin-interceptor работают как primary defense).

---

## Часть XII. Тестирование

### 12.1 Unit-тесты

В `internal/apps/kacho/api/<resource>/usecase_test.go` и `internal/handler/*_test.go`. Используют
моки port-интерфейсов из общего пакета `internal/repo/repomock`
(`fakeNetworkRepo`, `fakeProjectClient`, и т.д.). Worker-горутины
`operations.Run` дожидаются детерминированно через `repomock.AwaitOpDone` /
`AwaitAllOpsDone` (poll до `Operation.Done` с дедлайном 2s — не фиксированный `time.Sleep`).

Запуск: `make test-short`.

### 12.2 Integration-тесты

`internal/repo/integration_test.go` — testcontainers с Postgres 16.
Прогоняется только локально (`make test`); CI пропускает через `-short`.

Покрытие:
- Repo CRUD против реальной БД.
- EXCLUDE constraint поведение (CIDR overlap → 23P01).
- FK violations (Network с детьми → 23503).
- UNIQUE violations (duplicate name → 23505).
- Outbox emit транзакционность.

### 12.3 Newman E2E

Главная regression-инфраструктура — black-box покрытие всех публичных RPC.
Декларативный генератор: case-файлы на Python → `gen.py` → Postman-коллекции по сервису.

```
tests/newman/
├── cases/                       — декларативные case-наборы (Python), по сервису
│   └── {network,subnet,address,route-table,security-group,gateway,operation}.py
├── collections/                 — СГЕНЕРИРОВАННЫЕ Postman-коллекции (по сервису)
│   └── {…}.postman_collection.json
├── environments/local.postman_environment.json   — local stand (port-forward 18080)
├── scripts/
│   ├── gen.py                   — генератор коллекций из cases/* (источник истины — cases/)
│   └── run.sh                   — прогон одного/всех сервисов → out/{svc}.json + out/summary.txt
├── docs/                        — TAXONOMY / TEST-PLAN / CASES-INDEX / REQUIREMENTS / RESULTS (баги — в GitHub Issues)
└── out/                         — newman raw output (gitignored snap-логи)
```

Запуск: `python3 tests/newman/scripts/gen.py` (перегенерить коллекции) → `tests/newman/scripts/run.sh`
(все 8 сервисов; `--service network` для одного). Изоляция: каждый case — внутри своего
`runId`, suite — внутри pre-allocated `existingProjectId`/`existingProjectCrossId` (env), Account/Project
не создает. Требует `KACHO_VPC_DEFAULT_SG_INLINE=true` (default; иначе default-SG-кейсы краснеют).
Текущий результат — `tests/newman/docs/RESULTS.md`.

(Нагрузочные сценарии — рядом, `tests/k6/` (k6 HTTP + ghz gRPC Jobs),
baseline в `tests/k6/results/BASELINE.md`.)

### 12.4 Coverage

`coverage.out` и `coverage-full.out` — артефакты последнего прогона `make test`.

---

## Часть XIII. Зависимости от `kacho-corelib`

| Пакет corelib | Использование в kacho-vpc |
|---|---|
| `config` | `corecfg.Load(&Config)` с envconfig-тегами |
| `db` | `coredb.NewPool(ctx, dsn)` — pgxpool с дефолтными настройками |
| `grpcsrv` | `grpcsrv.NewServer(opts...)` — gRPC-сервер с reflection и interceptor-chain |
| `grpcclient` | factory для ProjectClient (TLS/insecure switch) |
| `ids` | `ids.NewID(prefix)` — генератор детерминированных id |
| `observability` | slog-logger |
| `operations` | `operations.New`, `operations.Run`, `operations.Wait`, `operations.Active`, `operations.NewRepo` |
| `outbox` | `outbox.Emit(ctx, tx, table, kind, id, eventType, payload)` |
| `errors` | sentinel-wrappers / matchers |
| `retry` | retry helper для transient gRPC ошибок |
| `shutdown`, `backoff` | вспомогательные |
| `audit` | `AuditLogger` (no-op в текущей фазе) |
| `migrations/common/0001_operations.sql` | синхронизируется через `make sync-migrations` |

---

## Часть XIV. Пошаговое воспроизведение проекта

### 14.1 Подготовка инфраструктуры

1. Завести polyrepo-структуру: sibling-репо `kacho-proto`,
   `kacho-corelib`, `kacho-vpc`, `kacho-api-gateway`, `kacho-iam`, `kacho-geo`,
   `kacho-deploy`.
2. В `kacho-proto` определить `.proto` для `kacho.cloud.vpc.v1.*`,
   `kacho.cloud.operation.v1.*`.
   Сгенерировать Go-stubs в `gen/go/...`.
3. В `kacho-corelib` обеспечить пакеты из таблицы §13.

### 14.2 Каркас сервиса

1. Создать `go.mod` для `github.com/PRO-Robotech/kacho-vpc` с replace-стрелками
   на `kacho-corelib` и `kacho-proto` (для локальной разработки).
2. Завести каталоги `cmd/vpc`, `internal/{config,domain,service,repo,clients,handler,migrations}`.
3. Описать `config.Config` с переменными из §2.4.

### 14.3 Доменный слой

1. По одному файлу в `internal/domain/` на каждую сущность из §5.1–5.4.
2. Никаких импортов кроме `time`, `kacho-proto` (если нужны enum-зеркала).

### 14.4 Слой service — port-интерфейсы

1. В `internal/apps/kacho/api/<resource>/iface.go` описать репо- и client-порты per-use-case
   (см. §3.3).
2. В `internal/apps/kacho/shared/serviceerr/errors.go` — sentinel-ошибки (см. §4.6).
3. В `internal/apps/kacho/shared/serviceerr/map.go` — `MapRepoErr` со стрипом sentinel-префикса
   (per-resource `mapRepoErr` — в `internal/apps/kacho/api/<resource>/helpers.go`).
4. В `internal/apps/kacho/config/validate.go` и CIDR-хелперах (`subnet/add_cidr_blocks.go` / `remove_cidr_blocks.go`) — общие хелперы.

### 14.5 БД-схема

1. Создать `internal/migrations/0001_initial.sql` с таблицами, индексами,
   constraint-ами, generated columns, trigger-ом outbox (см. §6). Partial UNIQUE
   `(project_id, name) WHERE name <> ''` для subnet/RT/SG/GW/Address — здесь же.
2. Использовать `btree_gist` для EXCLUDE-constraint'ов.
3. Все ресурсные id — `TEXT`, не UUID.
4. Любая дальнейшая схема — только новой миграцией (`0002`+), не правкой примененных.
5. Описать `migrations.go` с `//go:embed *.sql` для `embed.FS`.

### 14.6 Слой repo

1. По одному файлу на таблицу. Каждый реализует port-интерфейс из service.
2. В `outbox.go` — обертка `emitVPC` поверх `kacho-corelib/outbox.Emit`.
3. В `unique.go` — функции распознавания pg-error по SQLSTATE
   (`23505`, `23P01`, `23503`).
4. В `paging.go` — кодирование `(created_at, id)` в opaque base64 page_token.

### 14.7 Слой service — реализации

Для каждого ресурса повторить шаблон:

1. `New<Resource>Service(...)` принимает все необходимые порты.
2. `Get/List` — sync, читают через repo, возвращают domain.
3. `Create/Update/Delete` — sync-валидация → `operations.New` →
   `operations.Run(fn)` → `Operation` клиенту.
4. `doCreate/doUpdate/doDelete` — async worker. Внутри:
   - `ProjectClient.Exists` → `NotFound` если нет.
   - Domain-checks (Network exists, и т.п.).
   - `repo.Insert/Update/Delete`.
   - `emitVPC` внутри той же TX.
   - Возврат `anypb.New(proto)` или `&emptypb.Empty{}` для Delete.
5. Маппинг domain→proto в `domain<Resource>ToProto` функциях.

Для `AddressService`:

- `AllocateInternalIP` и `AllocateExternalIP` методы.
- `AddressPoolService.ResolvePoolForAddress` имплементирует cascade (§7.1).
- Двухфазный allocator: `usableIPv4Sweep` + `pickRandomIPv4`.

Для `SecurityGroupService`:

- `UpdateRules` использует xmin для optimistic concurrency.

### 14.8 Transport-слой

1. Public per-resource handler — `internal/apps/kacho/api/<resource>/handler.go`
   рядом со своим use-case'ом; internal/cross-cutting — в `internal/handler/`.
2. Handler — thin transport: parse req → call service → format resp.
3. Каждый Get/Update/Delete делает `AssertProjectOwnership(ctx, resource.ProjectID)`
   (`internal/tenant`) после `repo.Get` (через service).
4. В `internal/handler/tenant_interceptor.go` — unary и stream interceptors поверх
   `TenantCtx` и AuthMode.
5. В `internal/handler/internal_address_allocate_handler.go` — обертка над
   `AddressService.Allocate*`.
6. В `internal/handler/operation_handler.go` — `Get` / `Cancel` через `ops`.
7. Конвертация `Operation` в proto — пакет `internal/apps/kacho/shared/pbconv`
   (`OperationToProto`, с маппингом `CreatedBy`).
8. В `internal/handler/internal_maperr.go` — generic info-leak-safe mapper.

### 14.9 Composition root

В `cmd/vpc/main.go` собрать все (см. §3.8 + §11.2):

1. Load config.
2. Whitelist AuthMode (`dev/production/production-strict`); strict-проверки TLS+sslmode.
3. Pool + opsRepo + projectClient (gRPC к kacho-iam) + zoneClient (gRPC к kacho-geo).
4. Все `*Repo` и `*Service`.
5. Два `*grpc.Server` с interceptor-ами.
6. Регистрация handler-ов на нужный сервер.
7. Listener-ы, две горутины Serve, shutdown-горутина на `<-ctx.Done` с
   GracefulStop обоих + `operations.Wait(30s)`.
8. Возврат из runServe строго после `<-shutdownDone`.

### 14.10 Тестирование

1. Unit-тесты со своими `fake*Repo` моками портов — на каждый service-метод.
2. Integration-тесты в `repo/integration_test.go` с testcontainers — на FK,
   EXCLUDE, UNIQUE.
3. Newman-сьют в `tests/newman/` по схеме из §12.3.
4. Skeleton CI: `make test-short` (fast), `make test` (full).

### 14.11 Чек-лист итоговой проверки

| Пункт | Критерий готово |
|---|---|
| Сборка | `go build ./...` без ошибок |
| Линтер | `golangci-lint run` чистый |
| Миграции | `bin/kacho-vpc migrate up` идемпотентен |
| Service start | `bin/kacho-vpc serve` слушает 9090 и 9091 |
| Newman | `python3 tests/newman/scripts/gen.py && tests/newman/scripts/run.sh` — 0 failures (нужен port-forward api-gateway → 18080 + `KACHO_VPC_DEFAULT_SG_INLINE=true`) |
| Polling-наблюдение | Создание Network → `OperationService.Get` доходит до `done=true`, ресурс виден в `List` (Watch RPC нет) |
| Allocate IP | `InternalAddressService.AllocateExternalIP` возвращает IP из настроенного pool'а |
| Cascade | Изменение network-default binding / is_default-пула меняет выбираемый pool без рестарта |
| Graceful shutdown | SIGTERM завершает процесс в пределах 30 секунд, in-flight LRO дорабатывают |
| AuthMode production | Anonymous caller → `PermissionDenied` |
| EXCLUDE constraint | Параллельный Create двух Subnet с overlap → один успешный, один `FailedPrecondition` |
| Idempotent Allocate | Повторный Allocate того же address — same IP, `already_allocated=true` |
| Outbox TX atomicity | Если worker падает между INSERT ресурса и `emitVPC`, оба откатываются (одна TX) |
| garbage id | malformed/нераспознанный id (нет известного 3-char prefix `net/sub/adr/rtb/sgr/gtw/nic/apl/enp`) → sync `INVALID_ARGUMENT "invalid <res> id '<X>'"` (`corevalidate.ResourceID`). Well-formed-но-несуществующий → `NOT_FOUND` через `repo.Get`. Family-agnostic |
| timestamp truncation | Все `created_at` в proto-response обрезаны до секунд |
| empty mask | `UpdateNetwork` с пустой mask применяет mutable поля и игнорирует immutable из body |
| Operation response type | `Delete*` возвращают `Operation` с `response = google.protobuf.Empty` (а не `Delete*Metadata`) |
| Cross-tenant denied | Caller с `x-kacho-project-id: f1` не видит ресурс из `f2` → `PERMISSION_DENIED` |
| Internal listener camouflage | Non-admin caller на :9091 для не-Internal RPC получает `NOT_FOUND`, не `PERMISSION_DENIED` |

---

## Приложения

### A. Карта файлов проекта

```
kacho-vpc/
├── cmd/vpc/main.go                — composition root (gRPC servers + wiring)
├── cmd/migrator/main.go           — отдельный бинарь миграций
├── internal/
│   ├── config/config.go           — Config + DSN (pgxpool / миграции)
│   ├── domain/                    — entities (см. §3.2)
│   ├── apps/kacho/api/<resource>/ — use-cases, slice-per-RPC (см. §3.3)
│   │   ├── <resource>/iface.go    — per-use-case port-интерфейсы
│   │   ├── network/               — create.go, default_sg.go, helpers.go, ...
│   │   ├── subnet/                — create.go, add_cidr_blocks.go, remove_cidr_blocks.go, ...
│   │   ├── address/               — create.go (allocator consts), allocate.go, ...
│   │   ├── addresspool/           — resolve.go (cascade resolve), ...
│   │   ├── networkinterface/      — create.go, update.go, helpers.go (validateNICAddressCardinality), ...
│   │   ├── routetable/, securitygroup/, gateway/, addresspool/ (InternalAddressPoolService)
│   │   ├── <resource>/handler.go    — public per-resource gRPC handlers
│   │   ├── shared/serviceerr/     — errors.go, map.go (MapRepoErr); per-resource mapRepoErr — в <resource>/helpers.go
│   │   ├── shared/macutil/mac.go  — MAC-аллокация (package macutil)
│   │   ├── config/validate.go     — общие проверки (CIDR host-bits, IP в CIDR)
│   │   └── <resource>/usecase_test.go — unit-тесты (моки из internal/repo/repomock)
│   ├── repo/kacho/pg/             — pgx adapters (см. §3.4)
│   │   ├── 9 *.go файлов (network.go, subnet.go, address.go, ...)
│   │   ├── repository.go          — общий pool/transactor
│   │   └── *_integration_test.go  — testcontainers
│   ├── clients/
│   │   └── iam_client.go (+ project_cache.go) — ProjectClient через gRPC к kacho-iam
│   ├── handler/                   — cross-cutting / internal transport (см. §3.5)
│   │   ├── operation_handler.go             — OperationService.Get/Cancel
│   │   ├── internal_address_allocate_handler.go — InternalAddressService (IPAM)
│   │   ├── internal_network_handler.go     — InternalNetworkService (vrf_id / default-SG)
│   │   ├── tenant_interceptor.go  — AuthN/AuthZ scaffolding
│   │   └── internal_maperr.go     — info-leak-safe mapper
│   └── migrations/
│       ├── 0001_initial.sql       — baseline schema
│       ├── 0002_…sql … 0009_…sql  — инкрементные миграции
│       └── migrations.go          — embed.FS
├── deploy/                        — Helm chart
├── docs/architecture/             — детальные арх-документы
├── docs/ARCHITECTURE.md           — этот документ
├── tests/newman/                  — E2E/regression suite (Postman, генерится из cases/*.py)
├── tests/k6/                      — нагрузочные сценарии (k6 + ghz Jobs, см. tests/k6/README.md)
├── Makefile, Dockerfile, README.md
└── go.mod, go.sum
```

### B. Глоссарий

| Термин | Значение |
|---|---|
| LRO | Long-Running Operation — асинхронная операция с poll-интерфейсом |
| Outbox | Транзакционный журнал событий в той же БД, что и ресурсы |
| Cascade resolve | Многошаговый алгоритм выбора AddressPool для аллокации |
| Containment | `selector_labels @>` — pool описывает whitelist допустимых labels |
| xmin OCC | Optimistic concurrency control через системную колонку Postgres `xmin` |
| AuthMode | Уровень строгости AuthN-проверок (`dev/production/production-strict`) |
| TenantCtx | Caller identity, извлекаемый из gRPC metadata, кладется в context |
| Composition root | Единственное место сборки зависимостей (`cmd/vpc/main.go`) |

### C. Связанные документы

| Документ | Содержание |
|---|---|
| `docs/architecture/00-overview.md` | Высокоуровневое описание |
| `docs/architecture/01-resources.md` | Детально по каждому ресурсу |
| `docs/architecture/02-data-flows.md` | Sequence-диаграммы по сценариям |
| `docs/architecture/03-ipam.md` | IPAM модель и cascade |
| `docs/architecture/04-api-surface.md` | Все RPC и REST endpoints |
| `docs/architecture/05-database.md` | Схема БД и история миграций |
| `docs/architecture/06-conventions.md` | Правила и lesson-learned |
| `README.md` | Quick-start и руководство контрибьютора |
| `docs/architecture/07-known-divergences.md` | Registry осознанных дизайн-решений Kachō VPC |
| GitHub Issues (`github.com/PRO-Robotech/kacho-vpc/issues`) | Outstanding tech-debt / баги / задачи |
