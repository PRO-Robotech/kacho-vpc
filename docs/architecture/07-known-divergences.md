# Намеренные дизайн-решения Kachō VPC

Это **не баги** и **не задачи** — осознанные решения формы API и поведения, которые
могут удивить ревьюера. Файл существует, чтобы их не «фиксили» по второму разу. Все
решения описаны в собственных терминах Kachō (конвенции API — `06-conventions.md`).

## 1. NetworkInterface — first-class ресурс VPC

NIC — отдельный ресурс домена VPC (`NetworkInterfaceService`
Get/List/Create/Update/Delete/ListOperations), а не вложенная часть Instance.
Принадлежит `Subnet`, ссылается на `Address`-ресурсы по id, привязывается к
Compute-Instance через `nic_id`. Multi-IP на VM собирается из нескольких NIC.
Проекция — lean, control-plane-only (инфра/data-plane полей нет).

## 2. Опциональные поля на Create

- **`Subnet.v4_cidr_blocks` опционально** — CIDR-less подсеть легальна, CIDR
  добавляется позже через `:addCidrBlocks`. Internal-v4-allocate в CIDR-less подсеть →
  `FailedPrecondition "subnet ... has no IPv4 CIDR"`.
- **`SecurityGroup.network_id` опционально** — network-unbound (project-level) SG
  легальна; NIC принимает такие SG, если они того же project. Default-SG-на-сети
  всегда ставит непустой `network_id`.

## 3. IPv6 — симметрично IPv4

`:addCidrBlocks`/`:removeCidrBlocks` принимают и `v6_cidr_blocks`; `UpdateSubnet`
несет `v6_cidr_blocks` как soft-immutable / no-op (зеркало v4). Internal IPv6 —
`Address.internal_ipv6_address` oneof + `InternalAddressService.AllocateInternalIPv6`.

## 4. ListOperations переживает удаление ресурса

Для Network/Subnet/Address/NetworkInterface `ListOperations` отдает историю даже
после удаления самого ресурса: precondition `repo.Get` убран (handler best-effort —
жив → project-ownership; NotFound → пропуск). Строки `operations` не каскадятся.
RouteTable/SecurityGroup/Gateway по-прежнему гейтят на `repo.Get`.

## 5. Geography (Region/Zone) — не в kacho-vpc

Канонический владелец Geography — leaf-сервис `kacho-geo`. В `kacho-vpc` `zone_id`-колонки
(`subnet.zone_id`, `address_pool.zone_id`, `address.external_ipv4.zone_id`) — `TEXT`-id
без FK, валидируются на request-path через `geo.v1.ZoneService.Get`.

## 6. REST-пути неоднородны по форме — НЕ нормализовать

Стиль `google.api.http`-аннотаций в `.proto` (`kacho-proto`) намеренно смешанный:
kebab у custom-методов (`:addCidrBlocks`), child-list под ресурсом, camelCase у
top-level (`routeTables`, `securityGroups`, `addressPools`), `/operations/{id}` без
`/vpc/v1/`-префикса. Это зафиксированная форма поверхности API — «причесывание»
сломает контракт. Карта путей — `04-api-surface.md`.

## 7. Тело ошибки JSON-transcoding — стандартный JSON `{code,message}`

На неверный тип JSON-поля (`description`=число, `labels`=строка, oneof `address_spec`
задан дважды) api-gateway отдает стандартный grpc-gateway error-handler с JSON
(`400` + непустое тело). Кастомный error-handler ради иного формата тела не делаем.
Кейсы `*-CR-VAL-DESC-INT-TYPE` / `*-CR-VAL-LABELS-STRING-TYPE` / `ADR-CR-VAL-BOTH-SPEC` —
defensive (фиксируют `400` + непустое тело).

## 8. Пустое repeated-поле в List-ответе — отдается `[]`

`GET /vpc/v1/networks?projectId=<пустой>` → `{"networks":[]}` (а также
`nextPageToken:""`). Это `EmitUnpopulated`-настройка grpc-gateway marshaller'а; смена
затронула бы все ответы (напр. `done:false` в Operation), blast radius неоправдан.
Кейсы `NET-LST-*` — defensive (`j.networks || []`).

## 9. Subnet.Update с `v4CidrBlocks` в mask — no-op

Запрос принимается (`200`), но `repo.Update` CIDR-колонки не перезаписывает
(defensive depth). Менять CIDR существующей подсети в control-plane-only модели смысла
мало; реальное изменение — через `:addCidrBlocks`/`:removeCidrBlocks`. Кейс
`SUB-UPD-STATE-IMMUTABLE-CIDR` проверяет только `200`.

## 10. OperationService.Get/Cancel с bad id

malformed id → `InvalidArgument "invalid operation id '<X>'"`; well-formed id (известный
prefix, но бэкенд не подключен) → `NotFound "Operation <X> not found"`; id с prefix
домена с подключенным бэкендом → роутится туда. Реализация — `kacho-api-gateway`
`internal/opsproxy/proxy.go`.

## 11. Два error-mapper'а НЕ слиты в общий classifier — намеренно

Общий repo-sentinel→gRPC classifier — `serviceerr.classifyRepoSentinel`
(обёртки `MapRepoErr` для публичных сервисов и `MapRepoErrLeakSafe` для
Internal/admin-handler'ов). В него **сведены** три бывших full-switch дубликата
(`MapRepoErr`, `handler.internalMapErr`, `addresspool.mapPoolErr`). Ещё два
mapper'а оставлены отдельными **осознанно**:

- **`handler.mapAllocErr`** (`internal_address_allocate_handler.go`) — политика
  IPAM-allocate-пути намеренно **у́же** (compute→vpc внутренний edge): классифицируется только
  `ErrNotFound`, все прочие repo-ошибки (в т.ч. `ErrPoolNotResolved`,
  `ErrPoolExhausted`, `ErrInvalidIPv4`) сворачиваются в `Internal "internal
  allocator error"`. Это **другая** политика, чем superset-classifier
  (`ErrPoolNotResolved`→`FailedPrecondition`): маппинг на внутреннем edge влияет
  на retry-логику вызывающего Compute, менять его в рамках чисто-рефакторинга
  нельзя. Дублирования switch'а нет — функция узкая (NotFound + passthrough +
  fallback).
- **`handler.mapOpGetErr`** (`operation_handler.go`) — оперирует sentinel'ами
  **другого семейства**: `operations.ErrNotFound` / `operations.ErrAlreadyDone`
  из `kacho-corelib/operations`, а не `repo.Err*`. К repo-sentinel classifier'у
  отношения не имеет.

Если появится необходимость дать IPAM-allocate-пути богаче классификацию — это
поведенческое изменение внутреннего edge (нужен отдельный тикет + согласование с
Compute-retry), а не «причёсывание» дубликата.

## 12. `authn.trusted-forwarder=true` без server-mTLS — осознанный escape-hatch

В `authn.mode=production` (non-strict) публичный `:9090` listener принимает
identity caller'а (`x-kacho-principal-*` / `x-kacho-project-id`) как plaintext-
metadata, **если** оператор явно выставил `authn.trusted-forwarder=true` и НЕ
включил public server-mTLS. Это **намеренный** escape-hatch для деплоя за
аутентифицирующим forwarder'ом / service-mesh, который сам терминирует identity
до `:9090` (типовой ingress-mTLS / SPIFFE-mesh паттерн).

Гардрейлы, которые делают это безопасным-by-default:

- `authn.trusted-forwarder` по умолчанию `false` (fail-closed) — plaintext-
  principal без mTLS требует **явного** opt-in оператора.
- `ValidateServerMTLS` в production требует **ЛИБО** `PublicServerMTLS.Enable`,
  **ЛИБО** `trusted-forwarder=true` — «ни того ни другого» = отказ старта.
- `authn.mode=production-strict` **игнорирует** флаг: server-mTLS обязателен
  всегда (escape-hatch не действует). Для сред, где cryptographic binding identity
  к соединению обязателен, — это правильный режим.
- При активном escape-hatch на boot'е печатается WARN.

Trade-off осознан: при `trusted-forwarder=true` безопасность зависит от сетевой
изоляции `:9090` (NetworkPolicy / mesh-sidecar) — прямой доступ в обход forwarder'а
позволил бы подделать principal (CWE-290). Кто не может гарантировать сетевую
изоляцию — использует `production-strict` (server-mTLS). Дефолт менять на
«всегда требовать mTLS» нельзя: это сломало бы поддерживаемые mesh-деплои, где
identity терминируется вне процесса.

## 13. Dev-режим: internal listener + VRFID доступны анонимно — только вне production

Когда `authn.mode != production` **и** `authz.iam-endpoint` пуст, authz-interceptor
не навешивается (WARN-only), а `assertAdminAccess` пропускает анонимных caller'ов.
На internal `:9091` это делает `InternalAddressPoolService` (admin-CRUD пулов) и
`InternalNetworkService.GetNetwork` (отдаёт инфра-чувствительный `VRFID`)
доступными без authN/authZ. Это **намеренное** dev-поведение (локальный стенд /
port-forward / тесты без поднятого kacho-iam).

Production жёстко защищён и это **не** обходится:

- `authzWiringDecision` возвращает **fatal** (отказ старта), если в production
  IAM-endpoint отсутствует — анонимный admin в production невозможен.
- `ValidateServerMTLS` требует internal `:9091` server-mTLS в **ЛЮБОМ**
  production-режиме (не только strict; SEC-hardening r6 2026-07-05) — internal —
  это service→service, mTLS обязателен, trusted-forwarder escape-hatch на него не
  распространяется. Без mTLS на internal старт в production **отказывает**.
- Internal-only ресурсы (`AddressPool`, `VRFID`-несущий `GetNetwork`) по контракту
  живут только на cluster-internal `:9091`, который не публикуется на external TLS
  endpoint и не проксируется api-gateway на публичную поверхность (Запрет #6).

Требование к оператору: `:9091` в любом shared/staging окружении должен быть за
NetworkPolicy (cluster-internal), а authz включается выставлением `authz.iam-endpoint`.
«Скопировали dev-values на общий стенд» — конфиг-ошибка оператора, а не дефолт:
production-дефолт (`authn.mode=production`) fail-closed.

## 14. `cmd/vpc/runServe` — единый линейный composition root, намеренно длинный

`runServe` длинный (весь boot-sequence в одной функции): signal-setup, пулы
master/slave, ops-repo, метрики, mTLS load+validate, dial'ы vpc→iam / vpc→geo /
authz, list-filter, registrar/drainer, два gRPC-listener'а, graceful-shutdown.
CLAUDE.md **предписывает** `cmd/main.go` как **единственное** место wiring
(composition root) — размазывать инициализацию по пакетам запрещено. Длина —
следствие этого правила плюс плотных inline-комментариев с security-обоснованием
каждого fail-closed гардрейла (ValidateServerMTLS, mTLS-creds-ветвления,
breakglass-WARN).

Тело — почти линейная последовательность `create → defer Close()` без глубокого
ветвления; порядок `defer`-ов (pool/conn Close, cancel) значим и завязан на
scope самой `runServe`. Когезивные под-шаги уже вынесены в помощники
(`buildAuthorizeConn` / `buildListFilter` / `buildSyncRegistrar` /
`startRegisterDrainer` / `buildServices`). Дальнейшее «дробление ради длины» без
теста на composition root несёт ровно тот риск (сбитый порядок `defer`/bind-до-
guardrail), от которого предостерегает сам ресурс — поэтому не делается как
чистый рефакторинг. Новые под-шаги выносятся в помощник, только когда появляется
**самостоятельная** когезивная единица (как перечисленные выше), а не для
сокращения счётчика строк.

## 15. DTO record→proto реестр (`internal/dto`) — единый entry-point, boot-checked

Конвертация `record → proto` идёт через generic-реестр `internal/dto`
(`Transfer(FromTo(rec,&dst))`), а маппинг-реализации (`toproto/*.go`)
регистрируются `init()`-функциями в **выделенном** пакете `internal/dto/toproto`,
который composition root подключает blank-import'ом. Это **намеренный** дизайн, а
не tech-debt:

- Цель — **единый entry-point** вместо россыпи прямых `toproto.Network(d)`-хелперов
  по сервису (пакет-doc `dto/base.go`). Форма вывода proto идентична прямому вызову
  (контракт не меняется).
- `init()` вне `cmd/` — известное отступление от буквы `architecture.md` («no
  init()-side-effects outside cmd/»), но оно **локализовано** в одном dedicated
  пакете `toproto` (не размазано), и разрыв «зарегистрирован ли трансфер» закрыт
  **двумя** гардами: (а) compile-time closed-union `Transferrable` — вызвать
  `Transfer` с незарегистрированной парой `(F,T)` невозможно на этапе компиляции;
  (б) boot-time `dto.MustBeRegistered()` (зовёт composition root на старте) —
  потерянный blank-import → **паника на старте** (fail-fast), а не `codes.Internal`
  на первом live `Get/List`. То есть failure-mode — startup, как у миграций.
- Замена реестра на прямые exported-функции — поведенчески-нейтральный
  рефакторинг ~28 call-site'ов в ~16 файлах; выигрыш спорный (компайл-ошибка
  вместо boot-паники), а blast radius в security-ветке неоправдан. Если делать —
  отдельным clean-refactor тикетом с полным newman-прогоном, не в hardening-pass.

## 16. `AddressFamily` / `ResolvedPool` живут в use-case-пакете `addresspool`

Типы `AddressFamily` (`FamilyV4`/`FamilyV6`) и value-object `ResolvedPool`
объявлены в use-case-пакете `internal/apps/kacho/api/addresspool`
(`helpers.go`), а не в `internal/domain`. Use-case `address` потребляет их через
port `PoolService` (`address/iface.go`), из-за чего `address` импортирует
sibling use-case-пакет `addresspool` (`iface.go` / `allocate.go` / `create.go`)
ради этих определений типов.

Это **осознанный** выбор, а не layering-break: port-абстракция соблюдена
(impl `*addresspool.ResolverService` подключается в composition root; `address`
не зовёт `addresspool` напрямую), а единый источник enum'а family и
resolve-результата — сам pool-resolver, поэтому вызывающий код прозрачно
переиспользует его константы вместо параллельного типа (см. doc-комментарий
`address/iface.go`).

Trade-off: strict Clean Architecture предпочёл бы, чтобы сигнатуры port'а
оперировали `internal/domain`-типами, и тогда слайсы зависели бы только от
domain, а не друг от друга. Вынос `AddressFamily`/`ResolvedPool` в
`internal/domain` — поведенчески-нейтральный cross-package рефактор (трогает
`addresspool` helpers/resolve/poolHasFamily, `address` iface/allocate/create и
их тесты); выигрыш — компайл-когезия, риск в security-hardening-проходе
неоправдан. Делается отдельным clean-refactor тикетом, не здесь.

## 17. `List`/`ListByIDs` в pg-ридерах — рукописные, не сведены в общий builder

Каждый pg-ридер (`internal/repo/kacho/pg/{network,subnet,address,security_group,
route_table,gateway,network_interface}.go`) держит `List` и `ListByIDs` как две
почти идентичные рукописные сборки запроса (условия, `filter.Parse`-whitelist,
decode page-token, scan-loop, next-token). Пара расходится только предикатом
allowed-ID (authz-scoped `ListByIDs` vs unscoped `List`).

Это **следствие** ban #3 (no-ORM: только sqlc + handwritten pgx) — часть
hand-rolling'а неизбежна. Активного бага нет: filter-whitelist'ы пары
согласованы по всем ресурсам (напр. `subnet.go` `List`/`ListByIDs` оба
`["name","placement_type"]`); инвариант держится copy-paste-дисциплиной.

Trade-off: сведение обоих тел в один параметризованный helper (allowed-ID
предикат + per-resource колонки/whitelist как входы) убрало бы дрейф-риск
(правку whitelist в `List` без парной правки `ListByIDs`), но это
поведенчески-нейтральный рефактор ~16 тел во всех ресурсах — крупный blast
radius на list-пути в security-hardening-проходе. Как и #15, делается отдельным
clean-refactor тикетом с полным newman-прогоном, не здесь. До того парность
whitelist'ов держит регрессионный newman-слой (`*-LST-*` кейсы) и list-authz
CI-гейт (`make audit-list-filter`).

## 18. `Writer.Commit()`/`Abort()` финализируют TX на `context.Background()` — намеренно

`writerImpl.Commit()` / `Abort()` (`internal/repo/kacho/pg/repository.go`) вызывают
`tx.Commit(context.Background())` / `tx.Rollback(context.Background())`, а не request-ctx.
Порт `RepositoryWriter.Commit()`/`Abort()` намеренно БЕЗ ctx-аргумента: терминальная
финализация TX не должна отменяться отменой request-контекста.

Почему так, а не thread-ctx:

- commit по уже-отменённому ctx (клиент отвалился / deadline истёк ПОСЛЕ успешного DML)
  должен **завершиться**, иначе успешная бизнес-операция откатывается «из-за таймаута
  клиента» — хуже, чем чуть дольше подержать соединение. Rollback по Background
  симметричен: abort обязан отработать всегда.
- верхняя граница на всю обработку RPC уже стоит: `handler.Unary/StreamTimeoutInterceptor`
  (request-timeout, outermost) + bounded pgxpool (`pool_max_conns`) + statement/lock
  таймауты БД. Отдельный per-commit timeout добавил бы риск оборвать легитимно-медленный
  commit без выигрыша — blast-radius уже ограничен пулом.

Trade-off: под зависшим primary commit по Background ждёт нижних (pgx/OS) таймаутов, а не
request-deadline. Это осознанный выбор «всегда финализировать TX», а не пропущенная
ctx-propagation.

## 19. Три TTL-кеша (existence / project / list-filter) НЕ сведены в общий primitive

`internal/clients/existence_cache.go` (`existsCache` — positive-only, region/zone),
`internal/clients/project_cache.go` (`CachedProjectClient` — TTL+LRU pos/neg через
container/list, clock-inject) и `internal/authzfilter/filter.go` (`FGAFilter`-cache —
TTL + prefix-`Invalidate` + slice-deep-copy) держат по своей mutex+map+expiry-реализации.

Не сведены в один generic намеренно: три **разные** политики eviction/invalidation, а не
один primitive:

- `existsCache`: positive-only, без bound (region/zone — low-cardinality: рост ограничен
  числом зон/регионов в деплое, не unbounded на практике);
- `CachedProjectClient`: true-LRU c раздельными positive/negative TTL + clock-injection
  под unit-тесты;
- `FGAFilter`-cache: prefix-based `Invalidate(subject)` (LISTEN/NOTIFY-driven) + защитная
  deep-copy `AllowedIDs` на выдаче.

Единый `ttlcache[V]`, покрывающий LRU + clock + prefix-invalidation + copy-hook, нёс бы
больше policy-knob-сложности, чем убирает дублирования — net-negative для LEAN-цели.
Каждый кеш индивидуально оправдан (hot-path RTT-removal) и покрыт `-race` unit-тестом.
Сведение оправдано, только если появится 4-й кеш с той же политикой.

## 20. Migrator `Dialect`-интерфейс сохранён при единственной реализации — тонкий seam

`internal/apps/migrator` держит интерфейс `Dialect` + `DialectSpec` при единственной
реализации `postgresDialect` (продукт Postgres-only: Postgres 16, database-per-service).
Registry-таблица под один элемент, тип `dialectFactory`, `listDialects` и алиас
`ResolveDialect` **убраны** (LEAN r9b) — `NewDialect` резолвит прямой веткой.

Сам интерфейс оставлен намеренно (НЕ speculative multi-DB задел): `Runner` /
`Config.Dialect` зависят от него — тонкая обёртка делегирует Up/Down/Status/Create без
if-веток, изолируя goose-пакетные глобалки, и держит CLI-контракт `--dialect postgres`
(unknown → ошибка). Второй диалект добавляется реализацией интерфейса, если станет
реальным требованием.
