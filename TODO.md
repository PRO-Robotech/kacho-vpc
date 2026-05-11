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

## #9 — `cloud_id` / `organization_id` не в domain-моделях — `wontfix` (так в Yandex Cloud)

- **Проблема (как формулировалось):** VPC-ресурсы scoped только по `folder_id`;
  `cloud_id`/`organization_id` в схеме/domain отсутствуют, листинг — только по folder.
- **Проверено по reference YC** (`yandex/cloud/vpc/v1/*.proto`): в YC VPC API ресурсы
  (`Network`/`Subnet`/`Address`/`RouteTable`/`SecurityGroup`/`Gateway`/`PrivateEndpoint`)
  **тоже** несут только `folder_id` — ни `cloud_id`, ни `organization_id` на них нет.
  `List*Request` принимают ровно `folder_id` (required) — нет ни `cloud_id`, ни
  container-oneof; «список Network в Cloud» в YC делается клиентом: `resourcemanager.FolderService.List(cloud_id)` → затем `vpc.NetworkService.List(folder_id)` по каждому folder.
  `cloud_id` живёт на `resourcemanager.Folder`, `organization_id` — на `resourcemanager.Cloud`;
  cloud-level quota — внутренняя служба YC, на ресурсе VPC не отражена.
- **Решение:** **не делаем** — текущее состояние (folder-scoped, без `cloud_id` на ресурсах)
  и есть YC-parity. Добавление `cloud_id`/`organization_id` было бы **отклонением** от YC, а не
  его реализацией. Ad-hoc `FolderClient.GetCloudID` в IPAM-cascade — единственное место,
  где cloud_id вообще нужен (для cloud-level pool-selector), и это kacho-only расширение, не VPC API.
  Если когда-нибудь понадобится cloud-level листинг — это feature на стороне UI/agg-слоя
  (folders→networks), а не поле на VPC-ресурсе.

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

## Снапшоты newman (audit trail, не TODO)

- `tests/newman/out/*.json` — per-service JSON-reporter последнего прогона `tests/newman/scripts/run.sh`
  (агрегируется в `tests/newman/out/summary.txt`). Текущее (v16): **11 сервисов / ~731 кейс /
  3361 assertions / 0 fail** — incl. `internal-{pool,region-zone,cloud}` (admin IPAM RPC).
- История версий + mapping техник тестирования — `tests/newman/docs/RESULTS.md`;
  карта багов/наблюдений (FINDING-NNN) — `tests/newman/docs/BUG-MAP.md`;
  каталог уникальных паттернов — `tests/newman/docs/CASES-INDEX.md`.
- CI: job `newman` в `.github/workflows/ci.yaml` поднимает `kacho-deploy/ci/docker-compose.yml`
  (`make -C kacho-deploy ci-up`), сеет фикстуры, гоняет всю сьюту, fail если есть FAILED.
