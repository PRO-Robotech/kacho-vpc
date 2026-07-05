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
