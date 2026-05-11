# TODO — outstanding tech-debt

Только **открытые** пункты. Закрытые (`done`/`removed`/`reverted`) — в git-истории
коммитов и в `tests/newman/docs/BUG-MAP.md` / `docs/architecture/09-go-skills-applied.md`.
Каждый пункт: **Проблема** (что не так) → **Зачем фиксить** (что это даёт) →
**Почему отложено** (почему ещё не сделано / при каких условиях браться).

Нумерация пунктов — историческая (на неё ссылаются комментарии в коде и коммиты);
новые пункты добавлять со следующим свободным номером, не переиспользовать.

Status legend: `pending` / `partial` / `deferred` / `wontfix`.

> Закрыто в этом проходе: **#12** (`internal/ports/` + `internal/ports/portmock/` —
> единый пакет mock'ов портов), **#18** (newman E2E в CI — `kacho-deploy/ci/docker-compose.yml`
> + `make ci-up` + `.github/workflows/ci.yaml`), **#21** (`internal_watch_integration_test.go`),
> **#24** (`mustMarshalJSON` → error-returning), **#28** (`security_group_occ_integration_test.go`),
> **#35** (`tests/newman/cases/internal-{pool,region-zone,cloud}.py` — 45 кейсов),
> **#38** (`ipam_cascade_integration_test.go`). Плюс newman-FINDING-008 (ErrPoolNotResolved → FailedPrecondition).

---

## #3 + #4 — DNS-records для Address не реализованы — `deferred` (внешняя зависимость: kacho-dns)

- **Проблема:** в proto есть `Address.external_ipv4_address_spec.dns_record_specs`
  (на вход) и `Address.dns_records` (на выход), но сервис их **игнорирует** —
  `dns_record_specs` из Create/Update не маппятся в domain, `dns_records` в
  proto-ответе всегда пустой.
- **Зачем фиксить:** verbatim-YC parity — клиент, создающий Address с DNS-записью
  (PTR/A для внешнего IP), ожидает, что она применится и вернётся в Get. Сейчас
  «тихо проглатывается» — расхождение с reference YC.
- **Почему отложено:** требует отдельного сервиса `kacho-dns` (записи живут в DNS-зонах,
  не в VPC) — **ещё не реализован**, не трогаем. Parity не блокируется строго: YC API
  допускает пустой `dns_record_specs: []`, `dns_records` опциональны в ответе.
  Браться вместе с появлением `kacho-dns`.

## #9 — `cloud_id` / `organization_id` не используются в domain-моделях — `deferred`

- **Проблема:** все VPC-ресурсы scoped только по `folder_id`; `cloud_id`/`organization_id`
  в схеме и domain-структурах отсутствуют. Фильтрация/листинг — только по folder.
- **Зачем фиксить:** YC-семантика «список Network в Cloud» / cloud-level quota /
  cross-folder агрегация требуют знания cloud_id. Сейчас это резолвится ad-hoc
  через `FolderClient.GetCloudID` только в IPAM-cascade.
- **Почему отложено:** требует cross-service резолва из resource-manager + расширения
  proto **всех** ресурсов + миграции — большой churn в `kacho-proto` без текущего
  потребителя. Для folder-level scoping не нужно; ждёт явного требования cloud-level
  операций (cloud-list / cloud-quota / cross-folder агрегация в UI).

---

## Carry-over в IAM design phase — внешняя зависимость: kacho-iam (ещё не реализован)

Эти пункты ждут отдельной фазы дизайна IAM (JWT-validating interceptor заменит
текущий metadata-based scaffolding; контракт `TenantFromCtx` / `AssertFolderOwnership`
стабилен и переживёт замену). Пока `kacho-iam` нет — не трогаем.

### OperationService AuthZ — `deferred`

- **Проблема:** `OperationService.Get(operation_id)` не делает folder-ownership-check —
  любой аутентифицированный caller может прочитать любую Operation (и её `metadata`/`response`,
  где лежит snapshot ресурса). Все остальные публичные RPC проверяют ownership; Operation — нет.
- **Зачем фиксить:** info-leak / IDOR — через Operation можно узнать про ресурсы чужого folder.
- **Почему отложено:** требует data-model change — `folder_id` на таблице `operations`
  (которая в `kacho-corelib`, shared между всеми сервисами) либо резолв через
  `metadata.resource_id` → ресурс → folder. Кросс-репо изменение; делается в IAM-фазе.

### mTLS на internal listener `:9091` — `deferred`

- **Проблема:** `:9091` (Internal* RPC) защищён NetworkPolicy + admin-only interceptor +
  production-mode fail-closed, но без mTLS — любой pod, которому NetworkPolicy разрешает
  доступ, может вызывать internal RPC без аутентификации канала.
- **Зачем фиксить:** defense-in-depth — 4-й слой поверх трёх существующих; если NetworkPolicy
  мисконфигурена или обойдена, mTLS остаётся барьером.
- **Почему отложено:** основная защита уже есть (NetworkPolicy + admin-check + prod-mode);
  mTLS требует cert-management (issuer, ротация, mount в pod'ы) — `kacho-deploy`-scope +
  часть общего IAM/mesh-дизайна, не точечный фикс.

### `internal/handler/SECURITY.md` устарел — `deferred`

- **Проблема:** per-handler таблица точек fix'а AuthZ написана до закрытия #40–#44;
  описывает дыры, которых уже нет (production-mode, `AssertFolderOwnership`, info-leak-фиксы).
- **Зачем фиксить:** документ-ловушка — новый разработчик может «исправлять» уже исправленное
  или решить, что AuthZ вообще нет.
- **Почему отложено:** будет переписан целиком при IAM design phase (тогда же поменяется
  и архитектура AuthZ — нет смысла переписывать дважды). До тех пор — в шапке файла
  пометка «устарело, см. TODO».

### Сам IAM (JWT-validating interceptor) — `deferred`

- **Проблема:** AuthN сейчас — metadata-based scaffolding (`x-kacho-actor` / `x-kacho-folder-id`
  заголовки от reverse-proxy/sidecar); нет валидации токенов, нет реальной проверки
  членства в folder/cloud через resource-manager.
- **Зачем фиксить:** production-grade авторизация — без неё `production`-mode полагается на
  то, что upstream-proxy корректно проставил claims; нет defense если proxy мисконфигурен/обойдён.
- **Почему отложено:** отдельная большая фаза (JWT issuer, claims-extraction, folder-membership
  lookup, кэширование), `kacho-iam` ещё не реализован. Контракт `TenantFromCtx` /
  `AssertFolderOwnership` спроектирован так, чтобы interceptor можно было заменить без правок handler'ов.

---

## Архитектурные рекомендации (не блокирующие)

Не баги и не дыры — улучшения для следующего крупного рефактора. Сделать сейчас —
скорее over-engineering / workspace-wide изменение, чем «фикс проблемы».

### `replace ../kacho-corelib` / `../kacho-proto` в `go.mod`

- **Проблема:** локальные `replace`-директивы на соседние репо — сборка зависит от
  layout рабочей директории, релиз не воспроизводим без всего workspace на диске.
- **Зачем менять:** versioned modules + `go.work` дают воспроизводимые сборки и явные
  версии зависимостей между сервисами.
- **Почему отложено:** работает для polyrepo-dev-цикла; переход требует публикации
  corelib/proto как versioned-модулей + тэгов — **workspace-wide** решение, не per-service.

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
- **Зачем менять:** конструктор должен принимать всё нужное сразу; опциональность —
  через `nil`-параметр или отдельный конструктор.
- **Почему отложено:** второй setter (`SetAllocator`) уже убран — аллокатор inlined в
  `AddressService` (commit `5581316`). Оставшийся `SetSGRepo` — единственный; почистится
  заодно с выделением default-SG-логики или при composition-root рефакторе.

---

## Снапшоты newman (audit trail, не TODO)

- `tests/newman/out/*.json` — per-service JSON-reporter последнего прогона `tests/newman/scripts/run.sh`
  (агрегируется в `tests/newman/out/summary.txt`). Текущее (v16): **11 сервисов / ~731 кейс /
  3361 assertions / 0 fail** — incl. `internal-{pool,region-zone,cloud}` (admin IPAM RPC).
- История версий + mapping техник тестирования — `tests/newman/docs/RESULTS.md`;
  карта багов/наблюдений (FINDING-NNN) — `tests/newman/docs/BUG-MAP.md`;
  каталог уникальных паттернов — `tests/newman/docs/CASES-INDEX.md`.
- CI: job `newman` в `.github/workflows/ci.yaml` поднимает `kacho-deploy/ci/docker-compose.yml`
  (`make -C kacho-deploy ci-up`), сеет фикстуры, гоняет всю сьюту, fail если есть FAILED.
