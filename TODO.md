# TODO — outstanding tech-debt

Только **открытые** пункты. Закрытые (`done`/`removed`/`reverted`) — в git-истории
коммитов и в `TODO.md` (раздел «Найденные баги») / `docs/architecture/09-go-skills-applied.md`.
Каждый пункт: **Проблема** (что не так) → **Зачем фиксить** (что это даёт) →
**Почему отложено** (почему ещё не сделано / при каких условиях браться).

Нумерация пунктов — историческая (на неё ссылаются комментарии в коде и коммиты);
новые пункты добавлять со следующим свободным номером, не переиспользовать.

Status legend: `pending` / `partial` / `deferred` / `wontfix`.

**Actionable backlog, не упирающийся в несуществующие сервисы (`kacho-dns` / `kacho-iam`) —
пуст.** Ниже осталось только: DNS-блокеры (#3/#4), IAM design-phase carry-over,
один `wontfix` (workspace-wide migration — оставлен по явной просьбе), by-design
расхождения с verbatim-YC (documentation, не баги) и аудит-снапшоты newman.

> Закрыто в этом проходе: **#12** (`internal/ports/` + `internal/ports/portmock/` —
> единый пакет mock'ов портов), **#18** (newman E2E в CI — `kacho-deploy/ci/docker-compose.yml`
> + `make ci-up` + `.github/workflows/ci.yaml`), **#21** (`internal_watch_integration_test.go`),
> **#24** (`mustMarshalJSON` → error-returning), **#28** (`security_group_occ_integration_test.go`),
> **#35** (`tests/newman/cases/internal-{pool,region-zone,cloud}.py` — 45 кейсов),
> **#38** (`ipam_cascade_integration_test.go`). Плюс newman-FINDING-008 (ErrPoolNotResolved → FailedPrecondition).
>
> Закрыто во втором проходе (после закрытия зависших PR): **#9** убран (folder-scoped
> без `cloud_id`/`organization_id` — это и есть YC-parity; решение задокументировано в
> `CLAUDE.md` §2 и в «известных расхождениях» ниже); **`internal/handler/SECURITY.md` переписан**
> (актуализирован под текущее состояние — все public-handler'ы делают `AssertFolderOwnership`,
> `internalMapErr` без pgx-leak, env-gated DB/RM TLS, `production`/`production-strict` fail-closed;
> поэтому carry-over «SECURITY.md устарел» снят); **TTL-кеш на `FolderClient.GetCloudID`**
> (`folderCloudIDTTL` 10 мин — убрал RM gRPC RTT из hot-path external-IP-allocate; см. `tests/k6/results/BASELINE.md`);
> поправлены стейл-ссылки на уже-закрытые TODO (#1/#2/#6/#10/#17) в `CLAUDE.md` / `docs/architecture/*` / агент-доках.

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

> Сделано в этом проходе: **Anti-corruption layer / dedup конвертеров** (пакет
> `internal/protoconv` — один конвертер на ресурс вместо `domainXToProto`+`xToProto`;
> заодно закрыл created_at-drift в `Operation.response`), **декомпозиция composition root**
> (`cmd/vpc/main.go`: `buildServices`/`validateAuthMode`/`dialResourceManager`/`register{Public,Internal}Services`),
> **setter-DI** (`NetworkService.SetSGRepo` → конструкторный параметр).

### `replace ../kacho-corelib` / `../kacho-proto` в `go.mod` — `wontfix (workspace-wide migration)`

- **Проблема:** локальные `replace`-директивы на соседние репо — сборка зависит от
  layout рабочей директории, релиз не воспроизводим без всего workspace на диске.
- **Зачем менять (был бы):** versioned modules дают воспроизводимые сборки и явные
  версии зависимостей между сервисами.
- **Почему не делаем:** это **workspace-wide migration**, не per-service фикс — требует
  публикации `kacho-corelib`/`kacho-proto` как versioned-модулей с тэгами И дисциплины
  тэгирования на каждый их change (которой у проекта нет). Текущий setup (`replace` в каждом
  go.mod + `bootstrap.sh` клонирует siblings в `project/` + локальный gitignored `go.work`
  через `kacho-workspace/go.work.example`) — осознанный выбор для polyrepo-dev-в-одном-дереве.
  Браться вместе с релизной фазой проекта, не раньше.

---

## Найденные баги / наблюдения из тестов (единый реестр)

**Правило (см. `CLAUDE.md` §14):** всё, что найдено в newman / k6 / integration / unit-тестах
(баг, расхождение с verbatim YC, observability-gap), фиксируется **здесь**, а не в отдельных
bug-map'ах. Исправил — убираешь отсюда (фикс в коде + в commit message). Не баг / by-design /
documented divergence — оставляешь в разделе ниже с обоснованием.

### Исправленные (для истории — детали в git)

- **FINDING-005** — не было UNIQUE `(folder_id, name)` для Subnet/RT/SG/GW/PE/Address
  (только у Network). Fix: миграция `0002_resource_name_unique.sql` (partial UNIQUE `WHERE name <> ''`).
  Commit `ee07a7e`.
- **FINDING-008** — `ExplainResolution`/`AllocateExternalIP` на unresolvable input возвращали
  `13 INTERNAL` вместо `9 FAILED_PRECONDITION` (`service.ErrPoolNotResolved` не классифицировался
  в `internal_maperr.go`). Fix: добавлен case. Commit `f50413d`.
- **FINDING-006** — *не баг, ошибка теста*: кейс слал плоский `subnetId` (нет в proto PE);
  реальный `addressSpec.internalIpv4AddressSpec.subnetId` валидируется корректно. Кейс переписан.
- **created_at в `Operation.response`** — service-копии конвертеров не ставили `created_at`
  (Operation.response отдавал `null`), handler-копии ставили (truncate до секунд) → расхождение.
  Fix: единый пакет `internal/protoconv` (всегда truncate). Commit `9823941`.

### Известные расхождения / informational (не баги — by-design / documented)

- **Update/Delete/Move несуществующего ресурса → sync `404`, не async `Operation`** (verbatim YC
  делает async для Create, sync для остальных мутаций? — нет, у нас sync 404 от `AssertFolderOwnership`
  через `repo.Get` перед созданием Operation — без знания folder_id AuthZ невозможен). Intentional;
  задокументировано в proto-комментариях handler'ов + `docs/ARCHITECTURE.md` §4.1. (ex-FINDING-001)
- **REST-пути неоднородны** (kebab `:add-cidr-blocks` / `:move`, snake child-list `security_groups`/`route_tables`,
  `/operations/{id}` без `/vpc/v1/`, PE на `/endpoints`). Proto-decided (`google.api.http`); задокументировано
  в `docs/architecture/04-api-surface.md`. (ex-FINDING-002)
- **`OperationService.Get` с id без 3-char prefix → `400 INVALID_ARGUMENT "unknown prefix"`** (а не `404`).
  OpsProxy в api-gateway парсит prefix для маршрутизации — fail-fast перед роутингом. Спорно (пользователю
  `Operation X not found` ожидаемее), но архитектурно обосновано; нормализация к `404` — низкоприоритетная
  возможность (была REQ-004 в ex-REQUIREMENTS). (ex-FINDING-003)
- **`Address.GetByValue` несуществующего IP → `404 NOT_FOUND`** (а не `403`/`400`). Intentional —
  info-leak prevention: cross-tenant Get и nonexistent Get дают одинаковый 404. (ex-FINDING-004)
- **`InternalAddressPoolService.Create` без `name` → `200`, pool с `name=""`.** AddressPool — kacho-only
  admin resource (нет verbatim-YC аналога); unnamed pool валиден (резолв по id/labels, не по name) —
  VPC permissive name policy применима и к пулам. (ex-FINDING-007)
- **`InternalCloudService.SetPoolSelector` не проверяет существование `cloud_id`** — idempotent upsert,
  кросс-DB FK нет; «висячий» selector безвреден (без живых folder→cloud не зарезолвится). Proto-комментарий
  это отражает (исправлен). Реальная валидация потребовала бы `CloudService.Exists` RPC на resource-manager —
  не делаем (cross-repo фича). (ex-FINDING-009)
- **VPC-ресурсы folder-scoped, без `cloud_id`/`organization_id` на самих ресурсах** — это **verbatim-YC parity**,
  не gap: в YC VPC API `Network`/`Subnet`/`Address`/`RouteTable`/`SecurityGroup`/`Gateway`/`PrivateEndpoint`
  несут только `folder_id`, `List*Request` принимает ровно `folder_id`; «список Network в Cloud» — клиент:
  `resourcemanager.FolderService.List(cloud_id)` → `vpc.NetworkService.List(folder_id)` по каждому folder.
  Ad-hoc `FolderClient.GetCloudID` в IPAM-cascade — единственное место, где cloud_id нужен (cloud-level
  pool-selector), и это kacho-only расширение, не VPC API. Также в `CLAUDE.md` §2. (ex-#9)

---

## Снапшоты newman (audit trail, не TODO)

- `tests/newman/out/*.json` — per-service JSON-reporter последнего прогона `tests/newman/scripts/run.sh`
  (агрегируется в `tests/newman/out/summary.txt`). Текущее: **11 сервисов / ~731 кейс /
  3361 assertions / 0 fail** — incl. `internal-{pool,region-zone,cloud}` (admin IPAM RPC).
- История версий + mapping техник тестирования — `tests/newman/docs/RESULTS.md`;
  каталог уникальных паттернов кейсов — `tests/newman/docs/CASES-INDEX.md`.
  (Баги/наблюдения — выше в этом файле; отдельного `TODO.md` больше нет.)
- CI: job `newman` в `.github/workflows/ci.yaml` поднимает `kacho-deploy/ci/docker-compose.yml`
  (`make -C kacho-deploy ci-up`), сеет фикстуры, гоняет всю сьюту, fail если есть FAILED.
