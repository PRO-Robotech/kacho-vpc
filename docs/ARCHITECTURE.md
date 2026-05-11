# kacho-vpc — итоговый архитектурный документ

Документ описывает сервис `kacho-vpc` в его текущем виде с детализацией,
достаточной для повторного воспроизведения. Изложение идёт сверху вниз:
от системного контекста к компонентному уровню, далее — к поведенческим
паттернам, доменной модели, БД-схеме, API-поверхности, операционным
аспектам и шагам пересборки.

Стиль примеров — текст, таблицы и sequence-диаграммы. Кода в документе нет.

---

## Часть I. Системный контекст (C0–C1)

### 1.1 Назначение сервиса

`kacho-vpc` — control-plane сервис управления виртуальной сетью облачной
платформы Kachō. Он владеет жизненным циклом семи публичных доменных
ресурсов (Network, Subnet, Address, RouteTable, SecurityGroup, Gateway,
PrivateEndpoint) и встроенным IPAM (Region, Zone, AddressPool, CloudPoolSelector
плюс три binding-таблицы). Сервис **не** управляет реальным data plane —
он только хранит конфигурацию, валидирует её и эмитит события об изменениях.
Внешний контракт повторяет API Yandex Cloud VPC по форме и семантике.

### 1.2 Место в системе Kachō

Kachō — polyrepo. Каждый домен живёт в отдельном Go-репозитории. Внешние
клиенты ходят через `kacho-api-gateway` (gRPC-proxy + grpc-gateway REST).
Сервисы общаются по gRPC. У каждого — своя Postgres-БД, шаринг через
прямой SQL запрещён.

```
                           kacho-ui (SPA, REST/JSON)
                                   |
                                   v
                         kacho-api-gateway
                          /              \
                         v                v
              kacho-resource-manager   kacho-vpc
                  (Org/Cloud/Folder)   (этот сервис)
                         ^                ^
                         |                |
                         +-- gRPC ref-validation
```

`kacho-vpc` зависит от `kacho-resource-manager` только через один порт-интерфейс
(`FolderClient`) — проверяет существование folder и достаёт `cloud_id` для
IPAM-cascade. Никакой прямой доступ к чужой БД.

### 1.3 Соседи и контракты

| Сосед | Канал | Что делает |
|---|---|---|
| `kacho-api-gateway` | gRPC `:9090` → REST | Маршрутизирует публичные RPC, преобразует ошибки в HTTP-status |
| `kacho-resource-manager` | gRPC client | `FolderClient.Exists(folderID)`, `FolderClient.GetCloudID(folderID)` |
| `kacho-compute`, прочие IP-потребители | gRPC `:9091` | `InternalAddressService.AllocateInternalIP`, `AllocateExternalIP` |
| Внутренние подписчики на изменения | gRPC server-stream `:9091` | `InternalWatchService.Watch` отдаёт события из outbox |
| Postgres (своя БД `kacho_vpc`) | pgx + LISTEN/NOTIFY | Источник истины |
| Admin-инструменты (UI, curl/REST на api-gateway internal mux) | gRPC `:9091` через api-gateway internal listener | Управление Region/Zone/AddressPool |

### 1.4 Внешний контракт

Все мутации (`Create/Update/Delete/Move/AddCidrBlocks/...`) возвращают
`Operation` (long-running async). Клиент поллит `OperationService.Get(id)`
до `done=true`. Все чтения (`Get/List/...`) — синхронные.

Ошибки маппятся в стандартные gRPC-коды с текстом, совпадающим verbatim
с YC: `NOT_FOUND "Folder with id %s not found"`, `FAILED_PRECONDITION
"Subnet CIDRs can not overlap"`, и так далее.

### 1.5 Нефункциональные требования

| Свойство | Значение |
|---|---|
| Идемпотентность чтений | Полная (read-only, без побочных эффектов) |
| Идемпотентность мутаций | По `Operation.id`; повторный INSERT не делается |
| Изоляция БД-уровня | Read Committed (по умолчанию pg), критичные участки полагаются на EXCLUDE/UNIQUE |
| Цели по верности контракту | Verbatim YC parity на уровне regex, status codes, error texts |
| Graceful shutdown | До 30 секунд на drain LRO-worker'ов |
| Latency бюджет | Не зафиксирован формально; sync-валидация в request-path, async-IO в worker |
| Concurrent watch streams | Лимит `KACHO_VPC_WATCH_MAX_STREAMS` (по умолчанию 32) |

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
- Per-Watch-stream dedicated `pgx.Conn` (вне пула) с открытым `LISTEN`.

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
(Region/Zone/AddressPool/PoolSelector — admin/IPAM) проброшена тем же
api-gateway на cluster-internal listener (`/vpc/v1/{regions,zones,addressPools,...}`).
Internal listener :9091 закрыт NetworkPolicy и не виден на external TLS endpoint.
`kacho-vpc-controllers` упразднён в Phase 2 — раньше он подписывался на
`InternalWatchService` и дёргал `InternalAddressService.Allocate*`; теперь
IPAM-allocate и default-SG creation выполняются inline в service-слое.

### 2.4 Конфигурация (envconfig)

| Переменная | Default | Назначение |
|---|---|---|
| `KACHO_VPC_DB_HOST/PORT/USER/PASSWORD/NAME` | `localhost/5432/vpc/_/kacho_vpc` | Подключение к Postgres |
| `KACHO_VPC_DB_SSLMODE` | `disable` | `disable/require/verify-ca/verify-full` |
| `KACHO_VPC_DB_MAX_CONNS` | `0` (pgx default = `max(4, NumCPU)`) | Размер pgx pool'а. Прокидывается в DSN как `pool_max_conns` **только** для pgxpool — `migrate` использует `MigrateDSN()` без него (см. FINDING-007) |
| `KACHO_VPC_GRPC_PORT` | `9090` | Публичный gRPC |
| `KACHO_VPC_INTERNAL_PORT` | `9091` | Internal gRPC |
| `KACHO_VPC_WATCH_MAX_STREAMS` | `32` | Лимит конкурентных Watch |
| `KACHO_VPC_RESOURCE_MANAGER_GRPC_ADDR` | `resource-manager.kacho.svc.cluster.local:9090` | Endpoint RM |
| `KACHO_VPC_RESOURCE_MANAGER_TLS` | `false` | TLS на RM-канале |
| `KACHO_VPC_DEFAULT_SG_INLINE` | `true` | `true` — `Network.doCreate` синхронно создаёт default SG (workaround после упразднения controllers, verbatim YC). `false` — Network.Create НЕ создаёт SG (убирает 2 INSERT + 1 UPDATE из hot-path, +30-40% write-throughput; для load-тестов и deploy с внешним SG-reconciler'ом). При `false` newman-кейсы `*-LSG-CRUD-DEFAULT-SG` / `*-DEL-STATE-DEFAULT-SG` краснеют |
| `KACHO_VPC_AUTH_MODE` | `dev` | `dev / production / production-strict` |

`production-strict` требует `RESOURCE_MANAGER_TLS=true` и `DB_SSLMODE ∈
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

Жёсткое dependency rule: стрелки только сверху вниз и слева-направо.
`domain` и `service` не знают про `pgx`, `grpc`, `sqlc`. Port-интерфейсы
(`NetworkRepo`, `FolderClient`, ...) объявляются в `service/ports.go`,
их реализуют `repo/*` и `clients/*`. Wiring — в `cmd/vpc/main.go`,
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
| `private_endpoint.go` | `PrivateEndpoint` | DnsOptions jsonb |
| `address_pool.go` | `AddressPool` + `AddressPoolKind` | Глобальный (без `folder_id`) |
| `geography.go` | `Region`, `Zone` | Глобальные admin-ресурсы |
| `cloud_pool_selector.go` | `CloudPoolSelector` | Admin-controlled labels на Cloud |

### 3.3 Слой `service/`

Use-cases. Один файл на ресурс плюс общие модули.

| Файл | Содержимое |
|---|---|
| `ports.go` | Все port-интерфейсы: `NetworkRepo`, `SubnetRepo`, `AddressRepo`, `RouteTableRepo`, `SecurityGroupRepo`, `GatewayRepo`, `PrivateEndpointRepo`, `FolderClient`, `Pagination`, фильтры |
| `address_pool_ports.go` | Порты для IPAM (`AddressPoolRepo`, `AddressPoolBindingRepo`, `CloudPoolSelectorRepo`, `RegionRepo`, `ZoneRepo`) |
| `network.go` | `NetworkService` — Create/Update/Delete/Move/Get/List + ListSubnets/ListSecurityGroups/ListRouteTables/ListOperations |
| `subnet.go` | `SubnetService` — выше + AddCidrBlocks/RemoveCidrBlocks/Relocate/ListUsedAddresses |
| `address.go` | `AddressService` — выше + AllocateInternalIP/AllocateExternalIP + GetByValue/ListBySubnet |
| `route_table.go`, `security_group.go`, `gateway.go`, `private_endpoint.go` | Аналогично, с domain-специфичными методами |
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
| `network_repo.go` | `NetworkRepo` |
| `subnet_repo.go` | `SubnetRepo` (включая `SetCidrBlocks`, `SetZoneID`, `AddressesBySubnet`) |
| `address_repo.go` | `AddressRepo` (включая `GetByValue`, `SetIPSpec`) |
| `route_table_repo.go` | `RouteTableRepo` |
| `security_group_repo.go` | `SecurityGroupRepo` (включая `UpdateRules` с xmin-OCC, `UpdateRule`) |
| `gateway_repo.go` | `GatewayRepo` |
| `private_endpoint_repo.go` | `PrivateEndpointRepo` |
| `address_pool_repo.go` | `AddressPoolRepo` (включая cascade-SQL) |
| `address_pool_binding_repo.go` | `AddressPoolBindingRepo` (override/default привязки) |
| `cloud_pool_selector_repo.go` | `CloudPoolSelectorRepo` |
| `geography_repo.go` | `RegionRepo`, `ZoneRepo` |
| `outbox.go` | `emitVPC` — обёртка над `kacho-corelib/outbox.Emit` |
| `unique.go` | Распознавание SQLSTATE для unique/exclude/fk |
| `paging.go` | Кодирование/декодирование cursor-based page_token |
| `jsonb.go` | Хелперы для безопасной JSON-сериализации |

### 3.5 Слой `handler/`

Тонкий transport-слой. Один файл на gRPC-сервис.

| Файл | Сервис |
|---|---|
| `network_handler.go` | `NetworkService` (публичный) |
| `subnet_handler.go` | `SubnetService` |
| `address_handler.go` | `AddressService` |
| `route_table_handler.go` | `RouteTableService` |
| `security_group_handler.go` | `SecurityGroupService` |
| `gateway_handler.go` | `GatewayService` |
| `private_endpoint_handler.go` | `PrivateEndpointService` |
| `operation_handler.go` | `OperationService.Get` |
| `internal_watch_handler.go` | `InternalWatchService.Watch` |
| `internal_address_allocate_handler.go` | `InternalAddressService.AllocateInternalIP/External` |
| `internal_address_pool_handler.go` | `InternalAddressPoolService` |
| `internal_geography_handler.go` | `InternalRegionService`, `InternalZoneService` |
| `internal_network_handler.go` | `InternalNetworkService` (set/unset/get pool selector) |
| `internal_cloud_handler.go` | `InternalCloudService` (cloud-pool-selector CRUD) |
| `tenant_interceptor.go` | `TenantUnaryInterceptor`, `TenantStreamInterceptor`, `TenantCtx`, `AssertFolderOwnership` |
| `mapping.go` | `operationToProto` и общие конверторы |
| `internal_maperr.go` | Общий маппер ошибок internal-handlers без info-leak |

### 3.6 Слой `clients/`

Только `resourcemanager_client.go`: реализует `FolderClient` поверх
`grpc.ClientConn` к resource-manager. Используется в worker'ах сервисов
для existence-check и lookup `cloud_id`.

### 3.7 Слой `config/` и `migrations/`

- `config/config.go` — `Config` структура + `Load()` через `kacho-corelib/config`.
  Три DSN-метода: `baseDSN()` (стандартный postgres URL), `DSN()` (= base +
  `pool_max_conns` если `DBMaxConns>0`; для pgxpool), `MigrateDSN()` (= base, для
  goose/`database/sql.Open("pgx")` — иначе `pool_max_conns` уходит серверу как
  unknown PG-параметр → `FATAL`, см. FINDING-007).
- `internal/migrations/0001_initial.sql` — squashed baseline схемы (22 исторические
  миграции свёрнуты в один файл; embedded через `embed.FS`, объявлено в `migrations.go`).
- `internal/migrations/0002_resource_name_unique.sql` — partial UNIQUE
  `(folder_id, name) WHERE name <> ''` для subnets/route_tables/security_groups/
  gateways/private_endpoints/addresses (в `0001` был только `networks_folder_id_name_key`).
- `migrations/` (в корне репо) — staging для `make sync-migrations`
  (только `0001_operations.sql` от corelib, источник истины не здесь).

### 3.8 `cmd/vpc/main.go` — composition root

Единственное место, где собираются все сервисы и регистрируются handler-ы.
Порядок:

1. Чтение `Config`.
2. Открытие `pgxpool.Pool`.
3. Создание `operations.Repo` (corelib).
4. Открытие gRPC-клиента к resource-manager (`FolderClient`).
5. Инстанцирование `*Repo` объектов.
6. Инстанцирование `*Service` объектов с проброшенными портами.
7. Инстанцирование двух `*grpc.Server` (публичный и internal) с
   `TenantUnaryInterceptor` / `TenantStreamInterceptor`.
8. Регистрация всех handler-ов на оба сервера (см. таблицы в §8).
9. Запуск listener-ов в отдельных горутинах.
10. Блокировка `Serve` на публичном listener'е.
11. На SIGTERM — `GracefulStop` обоих серверов + `operations.Wait(30s)`,
    блокировка на `shutdownDone` перед возвратом из `runServe`.

`cmd/` содержит только `vpc/main.go` (composition root). Admin-операции над IPAM
(Region/Zone/AddressPool/pool-selector/bindings) — REST на cluster-internal
listener api-gateway (`/vpc/v1/{regions,zones,addressPools,...}`) либо web-UI;
отдельного CLI нет (`kachoctl-ipam` удалён).

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
     |                     |--Create()---------->|                      |               |
     |                     |                     |--sync validate------>|               |
     |                     |                     |--ops.Create(op)----->|               |
     |                     |<--Operation (done=false)                   |               |
     |<--Operation---------|                     |--go ops.Run(fn)----->|               |
     |                     |                                            |--Insert------>|
     |--Get(opId)--->...                                                |--emit outbox->|
     |                                                                  |--ops.SetDone()|
     |--Get(opId)----------------------------------------------------->done=true        |
```

Контракты worker'а:

- Возвращает `(*anypb.Any, error)`. На успех — `Any` с proto-ресурсом
  (для Create/Update) или `&emptypb.Empty{}` (для Delete).
- На ошибку sentinel-формы (`ErrNotFound`, `ErrAlreadyExists`, ...) —
  результат записывается в `operation.error` с правильным gRPC-кодом.
- Worker не должен panic-ить; `operations.Run` ловит panics.
- При SIGTERM `operations.Wait` ждёт завершения всех активных worker'ов
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
| `created_at` | timestamptz | now() |
| `processed_at` | timestamptz | Зарезервировано на будущее |

Триггер `vpc_outbox_notify_trg` на каждый INSERT выполняет
`pg_notify('vpc_outbox', sequence_no::text)`. Подписчики через
`InternalWatchService.Watch` получают пуш почти realtime.

**Транзакционная атомарность.** Repo-операции, эмитирующие outbox-событие,
обязаны:

1. Открыть транзакцию (`pool.BeginTx`).
2. Сделать INSERT/UPDATE/DELETE в ресурсной таблице.
3. Сделать `emitVPC(ctx, tx, kind, id, eventType, payload)` через ту же
   `pgx.Tx`.
4. COMMIT.

Если шаг 3 не в той же TX — событие может попасть в outbox без видимого
ресурса (или наоборот), что нарушит инвариант подписчиков. Pg_notify
шлётся **после COMMIT** автоматически (триггер срабатывает на INSERT в
outbox, но notification отправляется в момент commit'а транзакции).
Подписчик гарантированно увидит ресурс в БД к моменту обработки события.

### 4.3 Watch stream

`InternalWatchHandler.Watch` принимает `WatchRequest{from_sequence_no, kinds}`
и стримит обратно записи из outbox.

```
   subscriber                 watch handler              postgres
        |                          |                        |
        |--Watch(from=X, kinds=[]) |                        |
        |                          |--acquire slot-------->semaphore
        |                          |--pgx.Connect (2s)---->dedicated conn
        |                          |--LISTEN vpc_outbox--->pg
        |                          |--catchup SELECT------>events from X
        |<--stream batches---------|                        |
        |                          |--WaitForNotification->pg
        |                          |<-pg_notify-----------|
        |                          |--re-read since cursor>events
        |<--stream batch-----------|                        |
        |--cancel/disconnect------>|                        |
        |                          |--UNLISTEN + Close---->release
        |                          |--release slot-------->semaphore
```

Особенности:

- Соединение под `LISTEN` берётся отдельно через `pgx.Connect`, не из
  pool'а (иначе на abnormal exit conn вернётся в pool «грязным»).
- Connect под inner timeout 2 секунды; защита от удержания семафора при
  перегруженной БД.
- Семафор `streamSlot` ограничивает число конкурентных подписок.
- WaitForNotification под timeout 30 секунд для periodic re-poll
  на случай missed-notify (GC-пауза listener'а).
- Курсор шагает строго вперёд по `sequence_no`.
- Catchup batch size — 100 записей за один SELECT; loop продолжается до
  batch < 100 (end-of-data).

Формат `vpcv1.Event` на проводе:

| Поле | Тип | Семантика |
|---|---|---|
| `sequence_no` | int64 | Monotonic id, курсор для resume |
| `resource_kind` | string | `Network`, `Subnet`, `Address`, ... |
| `resource_id` | string | ID ресурса |
| `event_type` | string | `CREATED`, `UPDATED`, `DELETED` |
| `payload` | `google.protobuf.Struct` | Snapshot ресурса (raw jsonb → structpb через `json.Unmarshal → structpb.NewStruct`) |
| `created_at` | `google.protobuf.Timestamp` | Время записи в outbox |

`WatchRequest` принимает опциональный `kinds []string` — если задан, в
catchup-SELECT добавляется `AND resource_kind = ANY($2)`. Пустой `kinds`
= стримим всё.

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
allocator перевыбирает IP. Идемпотентность даёт безопасный retry на
сетевые сбои клиента.

### 4.5 Validation layering

Два уровня:

| Уровень | Когда | Что проверяет |
|---|---|---|
| Sync (до Operation) | В handler/service до возврата `Operation` | Required-поля, форматы (regex), CIDR host-bits, page_size, UpdateMask known-set, immutable-поля, deletion_protection |
| Async (внутри worker) | После `Operation` создана | Folder existence через `FolderClient`, Network/Subnet existence, FK violations, EXCLUDE constraint (CIDR overlap), UNIQUE violations (duplicate name) |

Sync-ошибки → `Operation` не создаётся, клиент получает gRPC-error сразу.
Async-ошибки → `Operation` помечается failed с `google.rpc.Status` в поле
`error`. Клиент видит результат при `OperationService.Get`.

### 4.6 Error mapping

Единая точка трансляции — `service/maperr.go::mapRepoErr`:

| Sentinel error | gRPC код | Семантика |
|---|---|---|
| `ErrNotFound` | `NOT_FOUND` | Ресурс или зависимость отсутствует |
| `ErrAlreadyExists` | `ALREADY_EXISTS` | Дублирующее имя в folder (UNIQUE 23505) |
| `ErrFailedPrecondition` | `FAILED_PRECONDITION` | CIDR overlap (EXCLUDE 23P01), сеть не пустая, deletion_protection |
| `ErrInvalidArg` | `INVALID_ARGUMENT` | Bad format, missing required field, mask violation |
| `ErrInternal` | `INTERNAL` | Default; текст не leak'ает pgx-detail |

Дополнительно `stripSentinel` снимает префикс sentinel из текста ошибки,
чтобы клиент видел verbatim YC-сообщение. Internal handlers используют
`internalMapErr` — обобщённый маппер с защитой от info-leak (sentinel-only
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
| Mask пустая | Full-object PATCH: применяются все mutable поля, immutable из тела silently игнорируются (verbatim YC) |

Immutable-поля по ресурсам:

| Ресурс | Immutable |
|---|---|
| Subnet | `v4_cidr_blocks`, `v6_cidr_blocks`, `network_id`, `zone_id` |
| Address | `external_ipv4_address_spec`, `internal_ipv4_address_spec`, `folder_id` |
| Прочие | `folder_id` (управляется через `Move`) |

### 4.9 AuthN/AuthZ scaffolding

В отсутствие IAM — interceptor читает metadata-headers:

| Header | Семантика |
|---|---|
| `x-kacho-actor` | Audit-only поле, идёт в логи; не даёт AuthZ-прав |
| `x-kacho-folder-id` (повторяемый) | Folder, к которому caller имеет доступ |
| `x-kacho-admin: true` | Cluster-wide админ, минует folder-check |

В context кладётся `TenantCtx{FolderIDs, Actor, Admin}`. Handler-ы
вызывают `AssertFolderOwnership(ctx, resource.FolderID)` после `repo.Get`
и до возврата ресурса/мутации.

Anonymous (нет ни Admin, ни FolderIDs) ведёт себя по-разному в зависимости
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
| Anonymous (нет ни Admin, ни FolderIDs) | Пропускается. В production-mode уже отвергнут вышестоящим guard-ом, в dev-mode это backward-compat для тестов без AuthN |
| Non-anonymous + Admin=true | Пропускается |
| Non-anonymous + Admin=false + method ∈ `/kacho.cloud.vpc.v1.Internal*` | `PERMISSION_DENIED "Permission denied"` |
| Non-anonymous + Admin=false + method не из Internal family | `NOT_FOUND "not found"` — camouflage, чтобы не светить структуру admin-listener'а |

Префикс-чек выполняется через `strings.HasPrefix`, а не `Contains` — это
защита от будущих сервисов со словом "Internal" в произвольной позиции
названия, которые могли бы случайно попасть в admin-listener.

---

## Часть V. Доменная модель

### 5.1 Публичные ресурсы (7)

Все folder-scoped, имеют общий минимум полей: `id`, `folder_id`, `created_at`,
`name`, `description`, `labels`.

| Ресурс | ID prefix | Доп. поля | Особенности |
|---|---|---|---|
| Network | `enp` | `default_security_group_id`, `route_distinguisher` | default SG создаётся inline в `doCreate` (опционально — `KACHO_VPC_DEFAULT_SG_INLINE`, default `true`) |
| Subnet | `e9b` | `network_id`, `zone_id`, `v4_cidr_blocks[]`, `v6_cidr_blocks[]`, `route_table_id`, `dhcp_options` | EXCLUDE constraint на `(network_id, v4_cidr_primary)` |
| Address | `e9b` | `addr_type`, `ip_version`, `reserved`, `used`, `deletion_protection`, `external_ipv4` (jsonb), `internal_ipv4` (jsonb) | Generated column `internal_subnet_id` для FK index |
| RouteTable | `enp` | `network_id`, `static_routes` (jsonb-массив) | Static-routes embedded |
| SecurityGroup | `enp` | `network_id`, `status`, `default_for_network`, `rules` (jsonb) | Rules embedded; обновления через xmin-OCC |
| Gateway | `enp` | `gateway_type` | Folder-level, не привязан к Network |
| PrivateEndpoint | `enp` | `network_id`, `subnet_id`, `address_id`, `ip_address`, `service_type`, `dns_options`, `status` | Кросс-refs (без FK на БД-уровне) |

**Замечание про префиксы.** Все VPC-ресурсы (Network, RouteTable, SecurityGroup,
Gateway, PrivateEndpoint) кроме Subnet/Address получают одинаковый 3-char
префикс `enp` — это умышленное design-решение для маршрутизации Operation.id
в api-gateway по первым 3 символам: gateway смотрит на префикс и направляет
`OperationService.Get(opId)` в нужный backend (VPC vs RM vs ...).
Subnet и Address используют `e9b`, чтобы отличить «дочерние» ресурсы от
«сетевых корней» в админ-инструментах. Operation у VPC сервиса получает
префикс `enp` (через `PrefixOperationVPC`).

### 5.2 IPAM-ресурсы (4 admin-only)

| Ресурс | ID prefix | Глобальный | Заметки |
|---|---|---|---|
| Region | id вида `ru-central1` | да | First-class сущность, seed-данные |
| Zone | id вида `ru-central1-a` | да | FK на Region (RESTRICT) |
| AddressPool | (без фиксированного префикса) | да | Не имеет `folder_id`; `kind` enum; `selector_labels` jsonb |
| CloudPoolSelector | PK = `cloud_id` | n/a | Не имеет своего id, ключ — Cloud |

### 5.3 Binding-таблицы (3)

| Таблица | PK | Семантика |
|---|---|---|
| `address_pool_address_override` | `address_id` | Привязка конкретного Address к конкретному пулу (explicit per-address) |
| `address_pool_network_default` | `network_id` | Default-pool для Network (explicit per-network) |
| `cloud_pool_selector` | `cloud_id` | Admin-labels Cloud для cascade-step |

### 5.4 Operations + Outbox + WatchCursors

| Таблица | Назначение | PK |
|---|---|---|
| `operations` | Long-running operations (синхронизирована с corelib) | `id` |
| `vpc_outbox` | Журнал событий | `sequence_no` |
| `vpc_watch_cursors` | Курсоры subscriber-ов (на будущее, если потребуется persistent cursor) | `subscriber_id` |

### 5.5 Связи между ресурсами (FK contract)

```
   Network (1) ──+── (N) Subnet ──── (N) Address[internal]
                 |
                 +── (N) RouteTable
                 |
                 +── (N) SecurityGroup ── (N) SecurityGroupRule (embedded)
                 |
                 └── default_security_group_id ─── soft ref (not FK)

   Address[external] ── folder-level, без Subnet
   Gateway           ── folder-level, без Network
   PrivateEndpoint   ── network_id + subnet_id + address_id (soft refs)
```

Реальные FK constraint-ы в схеме (10 штук):

| Источник | Колонка | Цель | ON DELETE |
|---|---|---|---|
| `subnets` | `network_id` | `networks(id)` | NO ACTION (default) |
| `route_tables` | `network_id` | `networks(id)` | NO ACTION (default) |
| `security_groups` | `network_id` | `networks(id)` | RESTRICT |
| `addresses` | `internal_subnet_id` (generated) | `subnets(id)` | RESTRICT |
| `address_pools` | `zone_id` | `zones(id)` | RESTRICT |
| `zones` | `region_id` | `regions(id)` | RESTRICT |
| `address_pool_address_override` | `address_id` | `addresses(id)` | CASCADE |
| `address_pool_address_override` | `pool_id` | `address_pools(id)` | RESTRICT |
| `address_pool_network_default` | `network_id` | `networks(id)` | CASCADE |
| `address_pool_network_default` | `pool_id` | `address_pools(id)` | RESTRICT |

Замечания:

- `Network → default_security_group_id` и кросс-ссылки `PrivateEndpoint
  → network_id/subnet_id/address_id`, `gateways`, `private_endpoints` —
  enforced на сервис-уровне (existence-check в worker), а **не** FK на
  БД-уровне. Это сознательное упрощение: removal default-SG ставит
  пустую строку в поле (а не NULL через SET NULL).
- NO ACTION в Postgres эквивалентен RESTRICT для DELETE по умолчанию —
  оба отвергают удаление родителя при наличии детей. Разница в
  deferred-режиме (CHECK момент в TX), что здесь не используется.
- CASCADE на binding-таблицах: удаление address или network автоматически
  удаляет их override/default; pool не удалится при наличии bindings
  (RESTRICT).

---

## Часть VI. БД-схема

### 6.1 Таблицы (16)

Source of truth — `internal/migrations/0001_initial.sql` (squashed
исторических 22-х миграций) + `0002_resource_name_unique.sql` (partial
UNIQUE на `(folder_id, name)` для 6 ресурсов):

| Таблица | Колонки (ключевые) |
|---|---|
| `operations` | `id text PK`, `description`, `created_at`, `created_by`, `done`, `metadata_type`, `metadata_data bytea`, `resource_id`, `response_type`, `response_data bytea`, `error_*` |
| `networks` | `id text PK`, `folder_id`, `created_at`, `name`, `description`, `labels jsonb`, `default_security_group_id`, `route_distinguisher` |
| `subnets` | `id`, `folder_id`, `created_at`, `name`, `description`, `labels`, `network_id`, `zone_id`, `v4_cidr_blocks text[]`, `v6_cidr_blocks text[]`, `route_table_id`, `dhcp_options jsonb`, `v4_cidr_primary cidr GENERATED`, `v6_cidr_primary cidr GENERATED` |
| `addresses` | `id`, `folder_id`, `created_at`, `name`, `description`, `labels`, `addr_type smallint`, `ip_version smallint`, `reserved`, `used`, `deletion_protection`, `external_ipv4 jsonb`, `internal_ipv4 jsonb`, `internal_subnet_id text GENERATED` |
| `route_tables` | `id`, `folder_id`, `created_at`, `name`, `description`, `labels`, `network_id`, `static_routes jsonb` |
| `security_groups` | `id`, `folder_id`, `network_id`, `created_at`, `name`, `description`, `labels`, `status`, `default_for_network`, `rules jsonb` |
| `gateways` | `id`, `folder_id`, `created_at`, `name`, `description`, `labels`, `gateway_type` |
| `private_endpoints` | `id`, `folder_id`, `created_at`, `name`, `description`, `labels`, `network_id`, `subnet_id`, `address_id`, `ip_address`, `service_type`, `dns_options jsonb`, `status` |
| `regions` | `id`, `name`, `created_at` |
| `zones` | `id`, `region_id`, `name`, `created_at` |
| `address_pools` | `id`, `name`, `description`, `labels`, `cidr_blocks text[]`, `kind smallint`, `is_default`, `zone_id`, `selector_labels jsonb`, `selector_priority` |
| `address_pool_address_override` | `address_id PK`, `pool_id`, `bound_at` |
| `address_pool_network_default` | `network_id PK`, `pool_id`, `bound_at` |
| `cloud_pool_selector` | `cloud_id PK`, `selector jsonb`, `set_at`, `set_by` |
| `vpc_outbox` | `sequence_no bigserial PK`, `resource_kind`, `resource_id`, `event_type`, `payload jsonb`, `created_at`, `processed_at` |
| `vpc_watch_cursors` | `subscriber_id PK`, `last_sequence_no`, `updated_at` |

### 6.2 Ключевые constraints

| Объект | Тип | Назначение |
|---|---|---|
| `subnets_no_overlap_v4` | `EXCLUDE USING gist (network_id WITH =, v4_cidr_primary inet_ops WITH &&)` | Race-free защита от CIDR-overlap внутри одной Network (только primary CIDR) |
| `subnets_no_overlap_v6` | Аналогично для v6 | То же для IPv6 |
| `addresses_external_ip_uniq` | UNIQUE на `external_ipv4 ->> 'address'` (partial) | Глобальная уникальность external IP |
| `addresses_external_pool_ip_uniq` | UNIQUE на `(external_ipv4 ->> 'address_pool_id', external_ipv4 ->> 'address')` (partial) | Race-free allocator поверх (pool, ip) |
| `addresses_internal_subnet_ip_uniq` | UNIQUE на `(internal_ipv4 ->> 'subnet_id', internal_ipv4 ->> 'address')` (partial) | То же для internal IP в Subnet |
| `networks_folder_id_name_key` | UNIQUE `(folder_id, name)` | Имя сети уникально в folder |
| `{subnets,route_tables,security_groups,gateways,private_endpoints,addresses}_folder_id_name_key` | UNIQUE `(folder_id, name)` WHERE `name <> ''` (миграция `0002_resource_name_unique.sql`) | Имя уникально в folder для остальных 6 ресурсов (verbatim YC); пустой `name` допускает несколько |
| `address_pools_zone_kind_default_uniq` | UNIQUE `(COALESCE(zone_id,''), kind)` WHERE `is_default=true` | Не более одного дефолтного пула на `(zone, kind)` |
| `zones_region_id_name_key` | UNIQUE `(region_id, name)` WHERE `name <> ''` | Имя зоны уникально в регионе |

### 6.3 Индексы (helper)

- `*_created_at_idx`, `*_folder_idx` — для пагинации и WHERE-фильтра.
- `*_network_idx`, `addresses_internal_subnet_idx` — для cascade-фильтров.
- `address_pools_selector_labels_gin` — GIN-индекс с `jsonb_path_ops` для
  `@>`-запроса в label-cascade.
- `cloud_pool_selector_gin` — то же для CloudPoolSelector.
- `vpc_outbox_seq_idx`, `vpc_outbox_kind_idx` — для catchup-запросов в Watch.
- `operations_resource_idx` — для `ListOperations` per-resource.

### 6.4 Triggers и функции

| Объект | Назначение |
|---|---|
| `vpc_outbox_notify()` PL/pgSQL | Шлёт `pg_notify('vpc_outbox', NEW.sequence_no::text)` |
| `vpc_outbox_notify_trg` AFTER INSERT | Вызывает функцию выше для каждого нового события |

Других триггеров нет — атомарность outbox обеспечивается одной TX на стороне приложения.

### 6.5 SQLSTATE → sentinel mapping

| SQLSTATE | Имя | Источник | Sentinel | gRPC код |
|---|---|---|---|---|
| `23505` | `unique_violation` | UNIQUE index (duplicate name, duplicate external IP) | `ErrAlreadyExists` | `ALREADY_EXISTS` |
| `23P01` | `exclusion_violation` | EXCLUDE constraint (CIDR overlap в subnets) | `ErrFailedPrecondition` | `FAILED_PRECONDITION` |
| `23503` | `foreign_key_violation` | FK на родителя или RESTRICT при удалении | `ErrFailedPrecondition` или `ErrNotFound` (зависит от контекста) | `FAILED_PRECONDITION` / `NOT_FOUND` |
| `23502` | `not_null_violation` | NOT NULL поле без значения | `ErrInvalidArg` | `INVALID_ARGUMENT` |
| `pgx ErrNoRows` | — | Get не нашёл строку | `ErrNotFound` | `NOT_FOUND` |
| прочие | — | Любая другая ошибка БД | `ErrInternal` (text не leak'ается) | `INTERNAL` |

Распознавание SQLSTATE в `repo/unique.go` через `pgconn.PgError.Code`.

### 6.6 Generated columns

| Таблица | Колонка | Выражение |
|---|---|---|
| `subnets` | `v4_cidr_primary` | Первый элемент `v4_cidr_blocks`, приведённый к `cidr` (STORED) |
| `subnets` | `v6_cidr_primary` | То же для v6 |
| `addresses` | `internal_subnet_id` | `internal_ipv4 ->> 'subnet_id'` если непусто (STORED) |

Generated STORED-колонки нужны индексам и EXCLUDE-constraint'у, которые
не умеют работать с выражениями `(jsonb_field)::cidr` напрямую.

---

## Часть VII. IPAM в деталях

### 7.1 Cascade resolve (5 шагов)

При `AllocateExternalIP(address_id)` сервис ищет первый подходящий пул
по следующему порядку:

1. **address override** — `address_pool_address_override[address_id]` → pool.
2. **network default** — `address_pool_network_default[address.network_id]` → pool
   (network_id у external Address берётся из проброшенного subnet или из
   связанной сети по контексту).
3. **cloud selector + label match** — для `cloud_id` (lookup через
   `FolderClient.GetCloudID(folder_id)`) берётся `cloud_pool_selector.selector`.
   Среди пулов с непустыми `selector_labels` отбираются те, где
   `selector_labels @> cloud.selector` (containment).
   Tie-break: `ORDER BY (jsonb_object_size(selector_labels) - cloud_size) ASC,
   selector_priority DESC LIMIT 1` — точнее (меньше разница) выигрывает,
   при равенстве — приоритет.
4. **zone default** — `address_pools` где `is_default=true`, `zone_id =
   address.zone_id`, `kind` совпадает.
5. **global default** — `address_pools` где `is_default=true`, `zone_id IS NULL`, `kind` совпадает.

Если ни один шаг не дал результата → `ErrPoolNotResolved` → `FAILED_PRECONDITION`.

### 7.2 Match-семантика (containment, не subset)

`cloud.selector ⊆ pool.selector_labels` (pool описывает **whitelist
разрешённых labels** для Cloud). Семантика inverse относительно
k8s NodeSelector — safe-by-default: неучтённая комбинация labels попадает
в default-pool, а не в специальный через subset-trick.

| Pool `selector_labels` | Cloud selector | Match? |
|---|---|---|
| `{tier=premium}` | `{}` | да |
| `{tier=premium}` | `{tier=premium}` | да |
| `{tier=premium}` | `{tier=premium, customer=acme}` | нет (`customer` не в pool) |
| `{tier=premium, customer=acme}` | `{tier=premium}` | да |

### 7.3 Двухфазный allocator

Параметры (`internal/service/address.go::const`):

| Константа | Значение | Смысл |
|---|---|---|
| `allocateRandomPhase` | 8 | Сколько попыток сделать random-pick до переключения на sweep |
| `allocateMaxAttempts` | 32 | Общий лимит попыток (random + sweep) |

**Фаза 1 — random pick (cheap path).** До `allocateRandomPhase` попыток
`pickRandomIPv4(cidr)` выбирает случайный host-IP из usable-диапазона;
делается `UPDATE addresses SET external_ipv4 = {ip, pool_id}`; при SQLSTATE
23505 — следующий random. Для low/medium occupancy сходится за 1–2 попытки.

**Фаза 2 — deterministic sweep с tried-set.** Если random за 8 попыток не
сошёлся (high occupancy: ≥95% занято), allocator переключается на
`usableIPv4Sweep(cidr, maxN)` — итерация подряд по host-IP, исключая
network/broadcast и уже tried-IP из фазы 1. Гарантированное закрытие за
конечное число попыток.

**Iterate по всем CIDR-блокам.** В `AllocateInternalIP` ходит по всем
`Subnet.V4CidrBlocks`, не только по `[0]` — критично для подсетей, которые
расширили через `AddCidrBlocks`. В `AllocateExternalIP` ходит по всем
`Pool.CIDRBlocks`.

Для типичного /24 pool'а с малой утилизацией выделение завершается за 1
SQL-запрос; на /20 c >90% утилизации — в пределах 32 попыток. Если ни
один candidate не подошёл — `ResourceExhausted`.

### 7.4 Идемпотентность

`AllocateInternalIP` и `AllocateExternalIP`:

- Lookup address.
- Если `external_ipv4.address` / `internal_ipv4.address` уже заполнен —
  возвращается existing с флагом `already_allocated=true`. Никаких
  повторных SQL-запросов.

Это даёт клиенту безопасный retry на сетевые таймауты.

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
| `NetworkService` | Create, Update, Delete, Move | async | Мутации, возвращают `Operation` |
| `SubnetService` | Get, List, ListOperations, ListUsedAddresses | sync | Чтения |
| `SubnetService` | Create, Update, Delete, Move, AddCidrBlocks, RemoveCidrBlocks, Relocate | async | Мутации |
| `AddressService` | Get, List, GetByValue, ListBySubnet, ListOperations | sync | Чтения |
| `AddressService` | Create, Update, Delete, Move | async | Мутации |
| `RouteTableService` | Get, List, ListOperations | sync | Чтения |
| `RouteTableService` | Create, Update, Delete, Move | async | Мутации |
| `SecurityGroupService` | Get, List, ListOperations | sync | Чтения |
| `SecurityGroupService` | Create, Update, Delete, Move, UpdateRules, UpdateRule | async | Мутации; xmin-OCC для UpdateRules |
| `GatewayService` | Get, List, ListOperations | sync | Чтения |
| `GatewayService` | Create, Update, Delete, Move | async | Мутации |
| `PrivateEndpointService` | Get, List, ListOperations | sync | Чтения |
| `PrivateEndpointService` | Create, Update, Delete | async | Мутации |
| `OperationService` | Get | sync | Pollable status |

### 8.2 Internal RPC (:9091)

| Service | RPC | Назначение |
|---|---|---|
| `InternalWatchService` | Watch (server-stream) | Outbox stream |
| `InternalAddressService` | AllocateInternalIP, AllocateExternalIP | IPAM из соседних сервисов |
| `InternalAddressPoolService` | CRUD пулов + Check/Explain | Управление IPAM |
| `InternalNetworkService` | SetPoolSelector, UnsetPoolSelector, GetPoolSelector | Network → label routing |
| `InternalCloudService` | SetCloudPoolSelector, UnsetCloudPoolSelector, GetCloudPoolSelector | Cloud-level labels |
| `InternalRegionService` | CRUD regions | Seed-данные / admin |
| `InternalZoneService` | CRUD zones | То же |

### 8.3 REST mapping (через api-gateway)

Публичные RPC проброшены api-gateway grpc-gateway'ем по схеме
`POST /vpc/v1/<resource>/...`. Internal RPC доступны только через
cluster-internal listener api-gateway и не публикуются на TLS-endpoint.

### 8.4 ID format

ID получается через `kacho-corelib/ids.NewID(prefix)` (см. таблицу в §5.1).
Колонки — `TEXT`. Никакой sync UUID-валидации: garbage id даёт async
`NOT_FOUND` (verbatim YC), а не sync `INVALID_ARGUMENT`.

---

## Часть IX. Sequence-диаграммы

### 9.1 Create Network с inline default-SG

```
client      api-gw    vpc.handler    vpc.service    folderClient    rm    networkRepo    sgRepo    outbox
  |           |           |              |               |          |         |            |          |
  |--POST----+|           |              |               |          |         |            |          |
  |           |--gRPC---->|              |               |          |         |            |          |
  |           |           |--Create()--->|               |          |         |            |          |
  |           |           |              |--validate---->|          |         |            |          |
  |           |           |              |--ops.New----->|          |         |            |          |
  |           |           |              |--ops.Create-->|          |         |            |          |
  |           |           |<--Operation(done=false)      |          |         |            |          |
  |<--Operation                                          |          |         |            |          |
  |                                                     async worker:                                  |
  |                       go fn()-->     |               |          |         |            |          |
  |                                      |--Exists()---->|--Get---->|         |            |          |
  |                                      |<--true                              |            |          |
  |                                      |--Insert(network)----------------->BEGIN          |          |
  |                                      |--Insert(default SG)------------------>           |          |
  |                                      |--Network UPDATE default_sg_id---->                          |
  |                                      |--emit outbox Network.CREATED-------------------->INSERT     |
  |                                      |--emit outbox SecurityGroup.CREATED-------------->INSERT     |
  |                                      |                                                COMMIT       |
  |                                      |--ops.SetDone()------------------------------------>         |
  |                                                                                                    |
  |--Get(opId)--+                                                                                      |
  |             |--gRPC---> ops.Get -->done=true, response=Network                                     |
  |<--Network---+                                                                                      |
```

> При `KACHO_VPC_DEFAULT_SG_INLINE=false` шаги `Insert(default SG)` /
> `Network UPDATE default_sg_id` / `emit SecurityGroup.CREATED` пропускаются —
> `Network.default_security_group_id` остаётся пустым (создание default SG
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
  |               |               |--cloud selector (by FolderClient.GetCloudID)    |
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

### 9.3 Watch outbox stream

```
subscriber      watch handler         pg (pool)       pg (dedicated)
   |                  |                  |                  |
   |--Watch(from=X)-->|                  |                  |
   |                  |--acquire slot--->|                  |
   |                  |--pgx.Connect---->|                  |
   |                  |                  |                  |--TCP open
   |                  |--LISTEN vpc_outbox------------------>|
   |                  |--SELECT * FROM vpc_outbox WHERE seq_no > X LIMIT 100
   |                  |                  |                  |
   |<--batch----------|                  |                  |
   |                  |  while batch_size == 100: повторить |
   |                  |--WaitForNotification (30s)--------->|
   |                  |                                     ⟵ pg_notify (от trigger)
   |                  |<-- notification                     |
   |                  |--SELECT new events---------+|       |
   |<--batch----------|                            +|       |
   |                  | ... loop                                |
   |--cancel--------->|                                         |
   |                  |--UNLISTEN + Close---------------------->|
   |                  |--release slot                           |
```

### 9.4 Subnet AddCidrBlocks (с EXCLUDE constraint защитой)

```
client       handler        service             subnetRepo      pg
  |            |              |                    |              |
  |--Add------>|              |                    |              |
  |            |--Add()------>|                    |              |
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
                    main goroutine        shutdown goroutine        watch goroutine     LRO worker
                          |                       |                      |                  |
SIGTERM ------>           |                       |                      |                  |
                          |--ctx cancel--->       |                      |                  |
                          |                       |--<-ctx.Done()        |                  |
                          |                       |--internalSrv.GracefulStop                |
                          |                       |                      ⟵-- ctx canceled  |
                          |                       |--grpcSrv.GracefulStop                    |
                          |                       |  (отвергает новые RPC, ждёт активные)    |
                          |                       |--operations.Wait(30s)                    |
                          |                       |                                          ⟵ ждём
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
(`0001_initial.sql` — squashed baseline; `0002_resource_name_unique.sql` —
partial UNIQUE на `(folder_id, name)`). При первом запуске goose создаёт
`goose_db_version` автоматически. `migrate` использует `cfg.MigrateDSN()` —
без `pool_max_conns` (иначе `database/sql` шлёт его серверу → `FATAL`, FINDING-007).

### 10.3 Health probe

Не реализован отдельный endpoint. Liveness — pgx ping в `coredb.NewPool`
при старте; readiness — успешный bind на gRPC-порту. В продовом deploy
используется `grpcurl` health check на reflection.

### 10.4 Observability

- Логи — `kacho-corelib/observability.NewSlogger` (slog в JSON или text).
- Метрики — не вынесены (TODO); ожидается prometheus exporter на отдельном порту.
- Trace — не реализован.

### 10.5 Деплой через Helm

`deploy/` содержит свой Chart.yaml + templates + values.yaml. Используется
umbrella-чартом `kacho-deploy` для dev-стенда (kind + Postgres + все сервисы).

---

## Часть XI. Безопасность

### 11.1 AuthN/AuthZ уровни

| Уровень | Защита |
|---|---|
| Транспорт (cross-service) | TLS на gRPC к resource-manager (`KACHO_VPC_RESOURCE_MANAGER_TLS`) |
| Транспорт (cross-service DB) | sslmode для pgx DSN |
| AuthN | Сейчас не реализован — IAM scaffolding через metadata headers, future — JWT |
| AuthZ (folder ownership) | `AssertFolderOwnership(ctx, folder_id)` во всех публичных handler-ах |
| AuthZ (admin operations) | `requireAdmin=true` interceptor на :9091 |
| Сетевая защита :9091 | k8s NetworkPolicy (allowlist namespaces) |
| Info-leak guard | `mapRepoErr`/`internalMapErr` не передают raw err.Error() в gRPC-text |

### 11.2 Fail-closed mode

`KACHO_VPC_AUTH_MODE=production` (или `production-strict`) делает interceptor
fail-closed: anonymous caller (нет Admin и нет FolderIDs) → `PERMISSION_DENIED`
сразу в interceptor, до handler-а. Это защищает от misconfigured deploy без
IAM sidecar — иначе anonymous = root.

### 11.3 production-strict дополнительно

- Требует `RESOURCE_MANAGER_TLS=true`.
- Требует `DB_SSLMODE ∈ {require, verify-ca, verify-full}` (не `prefer/allow`,
  потому что они допускают TLS-fallback к plaintext под MITM).
- Любое отклонение → процесс не стартует.

### 11.4 Известные ограничения / пробелы

- AuthN не реализован — service полагается на сетевой периметр + IAM
  sidecar (future).
- `OperationService.Get` не делает folder-AuthZ (требует data-model change —
  добавить `folder_id` в `operations` или join через `metadata.resource_id`).
- mTLS на :9091 — пока опциональный (TLS на listener'е возможен, но
  NetworkPolicy + admin-interceptor работают как primary defense).

---

## Часть XII. Тестирование

### 12.1 Unit-тесты

В `internal/service/*_test.go` и `internal/handler/*_test.go`. Используют
ручные моки port-интерфейсов (`internal/service/mock_test.go` —
`fakeNetworkRepo`, `fakeFolderClient`, и т.д.). Worker-горутины
`operations.Run` ждутся через `time.Sleep` (TODO — заменить на `assert.Eventually`).

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
│   └── {network,subnet,address,route-table,security-group,gateway,private-endpoint,operation}.py
├── collections/                 — СГЕНЕРИРОВАННЫЕ Postman-коллекции (по сервису)
│   └── {…}.postman_collection.json
├── environments/local.postman_environment.json   — local stand (port-forward 18080)
├── scripts/
│   ├── gen.py                   — генератор коллекций из cases/* (источник истины — cases/)
│   └── run.sh                   — прогон одного/всех сервисов → out/{svc}.json + out/summary.txt
├── docs/                        — TAXONOMY / TEST-PLAN / CASES-INDEX / REQUIREMENTS / RESULTS (баги — в TODO.md)
└── out/                         — newman raw output (gitignored snap-логи)
```

Запуск: `python3 tests/newman/scripts/gen.py` (перегенерить коллекции) → `tests/newman/scripts/run.sh`
(все 8 сервисов; `--service network` для одного). Изоляция: каждый case — внутри своего
`runId`, suite — внутри pre-allocated `existingFolderId`/`existingFolderCrossId` (env), Org/Cloud/Folder
не создаёт. Требует `KACHO_VPC_DEFAULT_SG_INLINE=true` (default; иначе default-SG-кейсы краснеют).
Текущий результат — `tests/newman/docs/RESULTS.md` (v15: 686 кейсов / ~3120 assertions / 0 fail).

(Старая quota-aware 3-suite сьюта против реального YC API — `newman_legacy/` — удалена;
история в git. Нагрузочные сценарии — рядом, `tests/k6/` (k6 HTTP + ghz gRPC Jobs),
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
| `grpcclient` | factory для FolderClient (TLS/insecure switch) |
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

1. Завести polyrepo-структуру: workspace + sibling-репо `kacho-proto`,
   `kacho-corelib`, `kacho-vpc`, `kacho-api-gateway`, `kacho-resource-manager`,
   `kacho-deploy`.
2. В `kacho-proto` определить `.proto` для `kacho.cloud.vpc.v1.*`,
   `kacho.cloud.vpc.v1.privatelink.*`, `kacho.cloud.operation.v1.*`.
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

1. В `internal/service/ports.go` описать репо- и client-порты для всех ресурсов
   (см. §3.3).
2. В `errors.go` — sentinel-ошибки (см. §4.6).
3. В `maperr.go` — функция `mapRepoErr` со стрипом sentinel-префикса.
4. В `validate.go` и `cidr_util.go` — общие хелперы.

### 14.5 БД-схема

1. Создать `internal/migrations/0001_initial.sql` с таблицами, индексами,
   constraint-ами, generated columns, trigger-ом outbox (см. §6).
2. Использовать `btree_gist` для EXCLUDE-constraint'ов.
3. Все ресурсные id — `TEXT`, не UUID.
4. `0002_resource_name_unique.sql` — partial UNIQUE `(folder_id, name) WHERE name <> ''`
   для subnet/RT/SG/GW/PE/Address (в `0001` был только `networks_folder_id_name_key`).
   Любая дальнейшая схема — только новой миграцией, не правкой применённых.
5. Описать `migrations.go` с `//go:embed *.sql` для `embed.FS`.

### 14.6 Слой repo

1. По одному файлу на таблицу. Каждый реализует port-интерфейс из service.
2. В `outbox.go` — обёртка `emitVPC` поверх `kacho-corelib/outbox.Emit`.
3. В `unique.go` — функции распознавания pg-error по SQLSTATE
   (`23505`, `23P01`, `23503`).
4. В `paging.go` — кодирование `(created_at, id)` в opaque base64 page_token.

### 14.7 Слой service — реализации

Для каждого ресурса повторить шаблон:

1. `New<Resource>Service(...)` принимает все необходимые порты.
2. `Get/List` — sync, читают через repo, возвращают domain.
3. `Create/Update/Delete/Move` — sync-валидация → `operations.New` →
   `operations.Run(fn)` → `Operation` клиенту.
4. `doCreate/doUpdate/doDelete` — async worker. Внутри:
   - `FolderClient.Exists` → `NotFound` если нет.
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

### 14.8 Слой handler

1. По одному файлу на gRPC-сервис.
2. Handler — thin transport: parse req → call service → format resp.
3. Каждый Get/Update/Delete делает `AssertFolderOwnership(ctx, resource.FolderID)`
   после `repo.Get` (через service).
4. В `tenant_interceptor.go` реализовать unary и stream interceptors с
   `TenantCtx` и тремя AuthMode.
5. В `internal_watch_handler.go` реализовать Watch с dedicated pgx.Conn,
   семафором, catchup + WaitForNotification.
6. В `internal_address_allocate_handler.go` — обёртка над `AddressService.Allocate*`.
7. В `operation_handler.go` — `Get` через `ops.Get`.
8. В `mapping.go` — `operationToProto` с обязательным маппингом `CreatedBy`.
9. В `internal_maperr.go` — generic info-leak-safe mapper.

### 14.9 Composition root

В `cmd/vpc/main.go` собрать всё (см. §3.8 + §11.2):

1. Load config.
2. Whitelist AuthMode (`dev/production/production-strict`); strict-проверки TLS+sslmode.
3. Pool + opsRepo + RM-client + folderClient.
4. Все `*Repo` и `*Service`.
5. Два `*grpc.Server` с interceptor-ами.
6. Регистрация handler-ов на нужный сервер.
7. Listener-ы, две горутины Serve, shutdown-горутина на `<-ctx.Done()` с
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
| Watch stream | Создание Network → событие в outbox → пришло в подписку |
| Allocate IP | `InternalAddressService.AllocateExternalIP` возвращает IP из настроенного pool'а |
| Cascade | Изменение CloudPoolSelector меняет выбираемый pool без рестарта |
| Graceful shutdown | SIGTERM завершает процесс в пределах 30 секунд, in-flight LRO дорабатывают |
| AuthMode production | Anonymous caller → `PermissionDenied` |
| EXCLUDE constraint | Параллельный Create двух Subnet с overlap → один успешный, один `FailedPrecondition` |
| Idempotent Allocate | Повторный Allocate того же address — same IP, `already_allocated=true` |
| Outbox TX atomicity | Если worker падает между INSERT ресурса и `emitVPC`, оба откатываются (одна TX) |
| Watch resume | Перезапуск подписчика с сохранённым cursor не пропускает события |
| garbage id | `GET /networks/garbageId` возвращает `NOT_FOUND` (async через repo.Get), а не sync `INVALID_ARGUMENT` |
| timestamp truncation | Все `created_at` в proto-response обрезаны до секунд |
| empty mask | `UpdateNetwork` с пустой mask применяет mutable поля и игнорирует immutable из body (verbatim YC) |
| Operation response type | `Delete*` возвращают `Operation` с `response = google.protobuf.Empty` (а не `Delete*Metadata`) |
| Cross-tenant denied | Caller с `x-kacho-folder-id: f1` не видит ресурс из `f2` → `PERMISSION_DENIED` |
| Internal listener camouflage | Non-admin caller на :9091 для не-Internal RPC получает `NOT_FOUND`, не `PERMISSION_DENIED` |
| Watch resource exhaustion | 33-й одновременный Watch (при лимите 32) получает `RESOURCE_EXHAUSTED` сразу |

---

## Приложения

### A. Карта файлов проекта

```
kacho-vpc/
├── cmd/vpc/main.go                — composition root (gRPC servers + wiring)
├── internal/
│   ├── config/config.go           — envconfig, Config + DSN()/MigrateDSN()
│   ├── domain/                    — 10 файлов entities (см. §3.2)
│   ├── service/                   — use-cases (см. §3.3)
│   │   ├── ports.go               — port-интерфейсы
│   │   ├── address_pool_ports.go  — IPAM ports
│   │   ├── network.go             — NetworkService
│   │   ├── subnet.go              — SubnetService
│   │   ├── address.go             — AddressService (с inline allocator)
│   │   ├── address_pool_service.go — AddressPoolService (cascade resolve)
│   │   ├── route_table.go, security_group.go, gateway.go, private_endpoint.go
│   │   ├── geography_service.go   — Region/Zone
│   │   ├── network_internal.go    — internal RPC backing
│   │   ├── errors.go, maperr.go, validate.go, cidr_util.go
│   │   └── *_test.go, mock_test.go
│   ├── repo/                      — pgx adapters (см. §3.4)
│   │   ├── 11 *_repo.go файлов
│   │   ├── outbox.go              — emit-обёртка
│   │   ├── unique.go              — SQLSTATE detection
│   │   ├── paging.go              — cursor codec
│   │   └── integration_test.go    — testcontainers
│   ├── clients/
│   │   └── resourcemanager_client.go — FolderClient через gRPC
│   ├── handler/                   — gRPC handlers (см. §3.5)
│   │   ├── 15 *_handler.go файлов
│   │   ├── tenant_interceptor.go  — AuthN/AuthZ scaffolding
│   │   ├── mapping.go             — operation/anypb conversion
│   │   └── internal_maperr.go     — info-leak-safe mapper
│   └── migrations/
│       ├── 0001_initial.sql       — squashed baseline schema
│       ├── 0002_resource_name_unique.sql — partial UNIQUE (folder_id, name)
│       └── migrations.go          — embed.FS
├── deploy/                        — Helm chart
├── docs/architecture/             — детальные арх-документы (00-09)
├── docs/ARCHITECTURE.md           — этот документ
├── tests/newman/                        — E2E/regression suite (Postman, генерится из cases/*.py)
├── tests/k6/                            — нагрузочные сценарии (k6 + ghz Jobs, см. tests/k6/README.md)
├── .claude/{agents,skills}/       — VPC-специфичные субагенты и скилы
├── Makefile, Dockerfile, README.md, TODO.md
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
| TenantCtx | Caller identity, извлекаемый из gRPC metadata, кладётся в context |
| Verbatim YC | Точное повторение API Yandex Cloud по форме, тексту ошибок, regex |
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
| `CLAUDE.md` | Service-specific инструкции для Claude Code |
| `README.md` | Quick-start и руководство контрибьютора |
| `TODO.md` | Outstanding tech-debt |
