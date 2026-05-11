# TODO — outstanding tech-debt

Только **открытые** пункты (закрытые `done`/`removed`/`reverted` — в git-истории
коммитов и в `tests/newman/docs/BUG-MAP.md` / `docs/architecture/09-go-skills-applied.md`).
Каждый пункт: **Проблема** (что не так) → **Зачем фиксить** (что это даёт) →
**Почему отложено** (почему ещё не сделано / при каких условиях браться).

Нумерация пунктов — историческая (на неё ссылаются комментарии в коде и коммиты);
новые пункты добавлять со следующим свободным номером, не переиспользовать.

Status legend: pending / partial / deferred / wontfix.

---

## #3 + #4 — DNS-records для Address не реализованы — `deferred`

- **Проблема:** в proto есть `Address.external_ipv4_address_spec.dns_record_specs`
  (на вход) и `Address.dns_records` (на выход), но сервис их **игнорирует** —
  `dns_record_specs` из Create/Update не маппятся в domain, `dns_records` в
  proto-ответе всегда пустой.
- **Зачем фиксить:** verbatim-YC parity — клиент, создающий Address с DNS-записью
  (PTR/A для внешнего IP), ожидает, что она применится и вернётся в Get. Сейчас
  «тихо проглатывается» — расхождение с reference YC.
- **Почему отложено:** требует отдельного сервиса `kacho-dns` (записи живут в DNS-зонах,
  не в VPC), которого нет в roadmap. Parity **не блокируется** строго — YC API
  допускает пустой `dns_record_specs: []`, и `dns_records` опциональны в ответе.
  Браться вместе с появлением `kacho-dns`.

## #9 — `cloud_id` / `organization_id` не используются в domain-моделях — `deferred`

- **Проблема:** все VPC-ресурсы scoped только по `folder_id`; `cloud_id`/`organization_id`
  в схеме и domain-структурах отсутствуют. Фильтрация/листинг — только по folder.
- **Зачем фиксить:** YC-семантика «список Network в Cloud» / cloud-level quota /
  cross-folder агрегация требуют знания cloud_id. Сейчас это резолвится ad-hoc
  через `FolderClient.GetCloudID` только в IPAM-cascade.
- **Почему отложено:** требует cross-service резолва из resource-manager + расширения
  proto всех ресурсов + миграции. Для текущего folder-level scoping не нужно;
  ждёт явного требования cloud-level операций.

## #12 — дублирование mock-port'ов между service- и handler-тестами — `deferred`

- **Проблема:** `internal/service/*_test.go` и `internal/handler/*_test.go` держат
  свои копии fake-реализаций одних и тех же port-интерфейсов (`NetworkRepo`,
  `FolderClient`, …). При изменении интерфейса нужно править оба места.
- **Зачем фиксить:** меньше boilerplate, единый источник fake'ов, нет drift между
  копиями (расходящиеся моки скрывают баги).
- **Почему отложено:** «правильное» решение — вынести port-интерфейсы в отдельный
  пакет `internal/ports/` (сейчас они в `internal/service/`, и handler не может
  импортировать service для тестов без циклической зависимости). Это structural
  refactor; не блокирует — дублирование терпимо при текущем размере.

## #18 — newman E2E не в CI — `partial`

- **Проблема:** `.github/workflows/ci.yaml` имеет job `newman`, но он `if: false` —
  нет шага, поднимающего локальный стенд (нужен Postgres + mock/real resource-manager
  + api-gateway port-forward). Регрессии ловятся только при ручном прогоне `tests/newman/scripts/run.sh`.
- **Зачем фиксить:** автоматическая защита от регрессий публичного API на каждый PR
  (сейчас 686 кейсов / ~3120 assertions гоняются вручную).
- **Почему отложено:** нужен `kacho-deploy/ci-up` target — лёгкий docker-compose
  (vpc + pg-vpc + stub resource-manager отвечающий на `FolderService.{Exists,GetCloudID}`,
  + api-gateway). Без него job не самодостаточен. Браться вместе с CI-compose в `kacho-deploy`.

## #21 — нет тестов на `InternalWatchService` — `deferred`

- **Проблема:** server-streaming RPC (outbox stream через `LISTEN/NOTIFY` + catchup
  + per-stream semaphore + dedicated `pgx.Connect`) покрыт только косвенно
  (newman делает Create → проверяет, что событие в `vpc_outbox`); сам стрим/catchup/
  resume-by-cursor/timeout-re-poll не тестируются.
- **Зачем фиксить:** Watch — единственный streaming-эндпоинт; регрессия в catchup-loop
  или semaphore-release незаметна до прода. Подписчики (UI/admin/в будущем kacho-compute)
  полагаются на «увидишь ресурс в БД к моменту обработки события».
- **Почему отложено:** требует testcontainers (реальный Postgres для `pg_notify`) +
  `bufconn` (in-process gRPC stream) — нетривиальная обвязка. Функционал работает
  (проверено вручную), приоритет ниже функциональных дыр.

## #24 — `mustMarshalJSON` паникует при ошибке `json.Marshal` — `wontfix`

- **Проблема:** хелпер `internal/repo/jsonb.go::mustMarshalJSON` делает `panic` если
  `json.Marshal` вернул ошибку (вместо возврата error). Теоретически — uncaught panic
  в repo-слое.
- **Зачем фиксить (был бы):** «error-returning» форма безопаснее «must»-формы по
  Go-конвенциям; panic в библиотечном коде — code smell.
- **Почему не фиксим:** для всех типов, которые сюда попадают (`map[string]string`
  labels, `[]Rule`, `*DhcpOptions`, `[]StaticRoute`, `ExternalIpv4Spec`, `InternalIpv4Spec`,
  `*DnsOptions`) `json.Marshal` **не может** упасть — только stdlib-типы, без
  `chan`/`func`/cyclic-ref. Panic unreachable. Переделка = defensive overhead без
  реальной пользы. Инвариант зафиксирован комментарием в `jsonb.go`; если когда-то
  добавится тип с потенциальным marshal-fail (например cyclic proto через `*anypb.Any`) —
  тогда переделать на error-returning.

## #28 — нет тестов на concurrent `SecurityGroup.UpdateRules` — `deferred`

- **Проблема:** lost-update защита через `xmin::text` OCC реализована (см. закрытый #22),
  но нет интеграционного теста, который параллельно делает два `UpdateRules` и
  проверяет, что один получает `Aborted`/конфликт, а не «тихо перезаписывает».
- **Зачем фиксить:** OCC легко сломать рефакторингом (забыть `WHERE xmin = $`);
  без теста регрессия пройдёт незамеченной → реальный lost-update в проде.
- **Почему отложено:** нужен testcontainers (две конкурентные транзакции к реальному
  Postgres). Добавляется в одном заходе с другими testcontainers-тестами (см. #21, #18).

## #35 — нет newman-сьюты для `InternalAddressPoolService` RPC — `wontfix`

- **Проблема:** newman покрывает только публичные (verbatim-YC) RPC; admin/IPAM
  internal RPC (AddressPool CRUD, pool-selector, bindings, Check/ExplainResolution)
  не покрыты декларативной сьютой.
- **Зачем фиксить (был бы):** регрессии admin-API тоже хочется ловить автоматически.
- **Почему не фиксим:** newman/Postman заточен под публичный REST через api-gateway;
  internal RPC — это kacho-only (нет в reference YC), доступны на cluster-internal
  listener, проще проверяются `grpcurl`/curl-скриптами или unit/integration-тестами
  service-слоя (`address_pool_service_test.go`). Тащить их в публичную сьюту — смешение
  ответственностей. Если понадобится — отдельный `tests/newman/cases/internal-*.py` или
  отдельный grpcurl-набор.

## #38 — нет полного e2e-теста IPAM cascade (все 5 шагов) — `pending`

- **Проблема:** cascade-резолв пула (`address_override` → `network_default` →
  `cloud_pool_selector` → `zone_default` → `global_default`) покрыт unit-тестами на
  каждый шаг по отдельности + newman smoke только на `global_default`. Нет одного
  сценария, где админ настраивает все 5 уровней и проверяется, что каждый Address
  попадает в правильный pool в зависимости от labels/folder/network.
- **Зачем фиксить:** cascade — главная нетривиальная фича VPC; приоритет шагов и
  inverse-containment match легко сломать. End-to-end тест ловит регрессию в
  взаимодействии шагов (а не только в отдельном шаге).
- **Почему отложено:** требует admin-настройки IPAM через REST (создать пулы/selector/bindings)
  + проверки allocate — то есть admin-REST-flow в тестах. Браться вместе с расширением
  newman/grpcurl на internal API (см. #35) либо как отдельный integration-тест.

---

## Carry-over в IAM design phase

Эти пункты ждут отдельной фазы дизайна IAM (JWT-validating interceptor заменит
текущий metadata-based scaffolding; контракт `TenantFromCtx` / `AssertFolderOwnership`
стабилен и переживёт замену).

### OperationService AuthZ — `deferred`

- **Проблема:** `OperationService.Get(operation_id)` не делает folder-ownership-check —
  любой аутентифицированный caller может прочитать любую Operation (и её `metadata`/`response`,
  где лежит snapshot ресурса). Все остальные публичные RPC проверяют ownership; Operation — нет.
- **Зачем фиксить:** info-leak / IDOR — через Operation можно узнать про ресурсы чужого folder.
- **Почему отложено:** требует data-model change — `folder_id` на таблице `operations`
  (которая в `kacho-corelib`, shared между всеми сервисами) либо резолв через
  `metadata.resource_id` → ресурс → folder. Кросс-репо изменение; ждёт IAM-фазы.

### mTLS на internal listener `:9091` — `deferred`

- **Проблема:** `:9091` (Internal* RPC) защищён NetworkPolicy + admin-only interceptor +
  production-mode fail-closed, но без mTLS — любой pod, которому NetworkPolicy разрешает
  доступ, может вызывать internal RPC без аутентификации канала.
- **Зачем фиксить:** defense-in-depth — 4-й слой поверх трёх существующих; если NetworkPolicy
  мисконфигурена или обойдена, mTLS остаётся барьером.
- **Почему отложено:** основная защита уже есть (NetworkPolicy + admin-check + prod-mode);
  mTLS требует cert-management (issuer, ротация, mount в pod'ы) — это `kacho-deploy`-scope +
  часть общего IAM/mesh-дизайна, не точечный фикс.

### `internal/handler/SECURITY.md` устарел — `deferred`

- **Проблема:** per-handler таблица точек fix'а AuthZ написана до закрытия #40–#44;
  описывает дыры, которых уже нет (production-mode, `AssertFolderOwnership`, info-leak-фиксы).
- **Зачем фиксить:** документ-ловушка — новый разработчик может «исправлять» уже исправленное
  или решить, что AuthZ вообще нет.
- **Почему отложено:** будет переписан целиком при IAM design phase (тогда же поменяется
  и архитектура AuthZ — нет смысла переписывать дважды). До тех пор — в шапке файла стоит
  пометка «устарело, см. TODO».

### Сам IAM (JWT-validating interceptor) — `deferred`

- **Проблема:** AuthN сейчас — metadata-based scaffolding (`x-kacho-actor` / `x-kacho-folder-id`
  заголовки от reverse-proxy/sidecar); нет валидации токенов, нет реальной проверки
  членства в folder/cloud через resource-manager.
- **Зачем фиксить:** production-grade авторизация — без неё `production`-mode полагается на
  то, что upstream-proxy корректно проставил claims; нет defense если proxy мисконфигурен/обойдён.
- **Почему отложено:** это отдельная большая фаза (JWT issuer, claims-extraction,
  folder-membership lookup, кэширование) — не точечный долг. Контракт `TenantFromCtx` /
  `AssertFolderOwnership` спроектирован так, чтобы interceptor можно было заменить без
  правок handler'ов.

---

## Архитектурные рекомендации (не блокирующие)

Не баги и не дыры — улучшения, которые имеет смысл сделать при следующем крупном рефакторе.

### `replace ../kacho-corelib` / `../kacho-proto` в `go.mod`

- **Проблема:** локальные `replace`-директивы на соседние репо — сборка зависит от
  layout рабочей директории, релиз не воспроизводим без всего workspace на диске.
- **Зачем менять:** versioned modules + `go.work` дают воспроизводимые сборки и явные
  версии зависимостей между сервисами.
- **Почему отложено:** работает для текущего polyrepo-dev-цикла; переход требует
  публикации corelib/proto как versioned-модулей и тэгов — workspace-wide решение,
  не per-service.

### Anti-corruption layer между handler и proto

- **Проблема:** handler-ы напрямую конвертят proto ↔ domain; `*ToProto`-конвертеры
  частично дублируются (service vs handler) → drift risk.
- **Зачем менять:** один слой конверсии, нет расхождений, проще менять proto без правок
  по всему handler-коду.
- **Почему отложено:** при текущей поверхности API дублирование терпимо; вводить ACL до
  роста числа ресурсов — over-engineering.

### Декомпозиция composition root

- **Проблема:** `cmd/vpc/main.go::run` — ~80 строк линейного wiring (repo'ы → service'ы →
  два gRPC-сервера → регистрация handler'ов → shutdown-горутина).
- **Зачем менять:** читаемость; меньше merge-конфликтов при добавлении ресурса; легче
  тестировать сборку по частям.
- **Почему отложено:** линейный wiring пока обозрим; разбивать на factory-функции — при
  следующем major refactor (или когда добавится ещё несколько сервисов).

### Setter-DI `networkSvc.SetSGRepo` — temporal coupling

- **Проблема:** `NetworkService` создаётся без `sgRepo`, потом (опционально, при
  `KACHO_VPC_DEFAULT_SG_INLINE=true`) `SetSGRepo` довешивает зависимость — два этапа
  инициализации, легко забыть второй.
- **Зачем менять:** конструктор должен принимать всё нужное сразу; опциональность лучше
  выразить через `nil`-параметр или отдельный конструктор.
- **Почему отложено:** второй setter (`SetAllocator`) уже убран — аллокатор inlined в
  `AddressService` (commit `5581316`). Оставшийся `SetSGRepo` — единственный; почистится
  заодно с выделением default-SG-логики или при composition-root рефакторе.

---

## Снапшоты newman (audit trail, не TODO)

- `tests/newman/out/*.json` — per-service JSON-reporter последнего прогона `tests/newman/scripts/run.sh`
  (агрегируется в `tests/newman/out/summary.txt`). Текущее: 686 кейсов / ~3120 assertions / 0 fail (v15).
- История версий + mapping техник тестирования — `tests/newman/docs/RESULTS.md`;
  карта багов/наблюдений (FINDING-NNN) — `tests/newman/docs/BUG-MAP.md`;
  каталог уникальных паттернов — `tests/newman/docs/CASES-INDEX.md`.
