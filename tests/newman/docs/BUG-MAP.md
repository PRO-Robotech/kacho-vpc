# Bug Map — newman findings

Регистр **продуктовых** наблюдений из прогонов newman (v16: ~731 кейсов / ~3360 assertions, 0 fail; см. RESULTS.md). Найдено и **исправлено** одно расхождение со
спекой (FINDING-005 — отсутствие UNIQUE `(folder_id, name)` для 6 VPC-ресурсов),
один кейс оказался ошибкой теста (FINDING-006). Остальные FINDING-NNN —
расхождения с canonical YC pattern / documentation-gap'ы (пограничные между
"design choice" и "improvement opportunity").

## Формат

См. README.md шаблон.

---

## Findings (наблюдения, не bug)

### FINDING-001 — Update/Delete возвращают sync 404, не async Operation

- **Severity**: documentation-gap
- **Found by**: NET-UPD-AUTHZ-NF-SYNC, NET-DEL-AUTHZ-NF-SYNC, SUB-UPD-AUTHZ-NF-SYNC и пр. (12 кейсов)
- **Status**: triaged — confirmed by code (intentional design)
- **Service**: kacho-vpc
- **Method**: NetworkService.{Update,Delete,Move}, аналогично для Subnet/Address/RT/SG/GW/PE
- **Symptoms**: `DELETE /vpc/v1/networks/<nonexistent-id>` возвращает sync `404` с `code:5 NOT_FOUND`,
  а не `200 + Operation`, у которого позже `error.code = 5`.
- **Expected (по аналогии с Create)**: 200 + async NotFound.
- **Actual**: sync 404.
- **Root cause**: handler делает sync `repo.Get` перед созданием Operation для
  `AssertFolderOwnership` (см. `internal/handler/network_handler.go::Update`/`Delete`).
  Без знания folder_id ресурса невозможно проверить AuthZ.
- **Verdict**: правильное design choice. Это **отличие от Create** (где folder_id есть в
  request body, AuthZ можно делать sync, существование ресурса — async).
- **Action**: задокументировать в proto-комментариях + workspace ARCHITECTURE.md
  как контракт. Тестам ожидать sync 404 — что и реализовано.

### FINDING-002 — REST-пути в proto: kebab-case vs camelCase

- **Severity**: documentation-gap
- **Found by**: SUB-ACB-CRUD-OK, SUB-LUA-CRUD-OK, SG-URL-CRUD-OK, PE-* (~15 кейсов)
- **Status**: documented
- **Service**: kacho-vpc / proto-mapping
- **Method**: множество
- **Symptoms**: gateway не отвечает на `:addCidrBlocks` (camelCase), `:updateRules`,
  `/usedAddresses`, `/privateEndpoints`. Реальные пути — kebab-case с двоеточием:
  `:add-cidr-blocks`, `:remove-cidr-blocks`, или `PATCH /rules`, или `/endpoints`.
- **Expected**: единая конвенция или ясная документация по REST-маппингу.
- **Actual**: смесь — `:add-cidr-blocks` (kebab), `:move` (одно слово), `/operations`
  (без `/vpc/v1/`), `/rules` (под /securityGroups/{id}/), `/endpoints` (вместо `/privateEndpoints`).
- **Action**: REQ-003 — единый документ REST endpoints map (см. REQUIREMENTS.md).

### FINDING-003 — OperationService.Get отвергает id без 3-char prefix с code 3 (InvalidArgument)

- **Severity**: documentation-gap
- **Found by**: OP-GET-NEG-NF-INVALID-PREFIX
- **Status**: triaged — confirmed (OpsProxy gateway behavior)
- **Service**: kacho-api-gateway
- **Method**: OperationService.Get
- **Symptoms**: `GET /operations/garbage-id` → `400` с `{"code":3, "message":"operation_id has unknown prefix: \"...\""}`.
- **Expected (по аналогии с resource Get)**: `404 NOT_FOUND` ("Operation X not found").
- **Actual**: `400 INVALID_ARGUMENT` "unknown prefix".
- **Root cause**: OpsProxy парсит 3-char prefix для маршрутизации; без known prefix
  не может выбрать backend. Это **отличие** от resource Get (там 404).
- **Verdict**: спорно. С точки зрения пользователя `Operation X not found` ожидаемее.
  С точки зрения архитектуры — fail-fast валидация перед маршрутизацией.
- **Action**: REQ-004 — нормализовать поведение к 404 NotFound на уровне OpsProxy
  middleware либо явно документировать как known divergence.

### FINDING-004 — Address.GetByValue → 404 NotFound для несуществующего IP

- **Severity**: cosmetic
- **Found by**: ADR-GBV-NEG-NF
- **Status**: confirmed (intentional)
- **Service**: kacho-vpc
- **Method**: AddressService.GetByValue
- **Symptoms**: запрос с несуществующим external IP даёт `404 NOT_FOUND`,
  не `403 PERMISSION_DENIED` или `400 INVALID_ARGUMENT`.
- **Verdict**: правильно. Информационная утечка через различение existed/not-existed
  закрыта тем что и `cross-tenant Get` и `nonexistent Get` дают одинаковый
  `404 NOT_FOUND` (см. TODO #50 в kacho-vpc/TODO.md).
- **Action**: no-op (правильное security design).

### FINDING-005 — нет UNIQUE (folder_id, name) для Subnet/RouteTable/SG/Gateway/PE/Address — FIXED

- **Severity**: High (parity break)
- **Found by**: `SUB-CR-NEG-DUP-NAME` (ex-`SUB-CR-CONF-DUP-NAME-FINDING-005`),
  `*-CR-NEG-DUP-NAME-CHECK` (add/gat/rou/sec)
- **Status**: **fixed** (2026-05-11)
- **Service**: kacho-vpc
- **Method**: `{Subnet,RouteTable,SecurityGroup,Gateway,PrivateEndpoint,Address}Service.Create`
- **Symptoms**: миграция `0001` создавала только `networks_folder_id_name_key`.
  Остальные 6 VPC-ресурсов допускали два ресурса с одинаковым непустым `name`
  в одном folder — расхождение с verbatim YC, где `name` уникален в folder.
- **Root cause**: при flat-rewrite (`0001`) UNIQUE-индекс был добавлен только
  для `networks`.
- **Fix**: миграция `internal/migrations/0002_resource_name_unique.sql` —
  partial UNIQUE `CREATE UNIQUE INDEX <table>_folder_id_name_key ON <table>
  (folder_id, name) WHERE name <> ''` для всех 6 таблиц. Empty name
  по-прежнему допускает несколько ресурсов (VPC permissive policy, verbatim YC).
  `repo.wrapPgErr` уже маппил `23505` → `service.ErrAlreadyExists` → gRPC
  `ALREADY_EXISTS` — repo-слой не менялся.
- **Verification**: ручной тест (две Subnet с одинаковым name, разный CIDR) →
  вторая Operation `error.code=6`. Newman `SUB-CR-NEG-DUP-NAME` зелёный.

### FINDING-006 — PE Create без subnet existence-validation — INVALID (test error)

- **Severity**: — (не bug)
- **Found by**: `PE-CR-NEG-SUBNET-NF-FINDING-006` (удалён)
- **Status**: **invalid** — ошибка в тесте, не в коде
- **Service**: kacho-vpc
- **Method**: `PrivateEndpointService.Create`
- **Symptoms (как казалось)**: `POST /vpc/v1/endpoints` с `{"subnetId": "<garbage>"}`
  завершался успешно — не было existence-проверки subnet.
- **Root cause**: в proto `CreatePrivateEndpointRequest` нет плоского поля
  `subnet_id`; subnet задаётся через `address_spec.internal_ipv4_address_spec.subnet_id`
  (oneof). grpc-gateway отбрасывает unknown-поле `subnetId` → `req.SubnetID==""` →
  ветка валидации `if req.SubnetID != "" { subnetRepo.Get(...) }` не выполняется →
  PE создаётся вообще без привязки к subnet (что допустимо). Сам код subnet
  валидирует корректно (`private_endpoint.go::doCreate` → `NOT_FOUND "Subnet ... not found"`).
- **Action**: тест-кейс переписан на verbatim-форму:
  - `PE-CR-NEG-SUBNET-NF` — `addressSpec.internalIpv4AddressSpec.subnetId = garbage` →
    async `NOT_FOUND`, verbatim `^Subnet .* not found$`.
  - `PE-CR-CRUD-WITH-SUBNET` — валидный subnetId → `address.subnetId` привязан в GET.
  - Прочие PE-кейсы по-прежнему используют плоский `subnetId` (молча игнорируется);
    это не bug, но потенциальная очистка test-фикстур — backlog, см. REQUIREMENTS.

### FINDING-007 — InternalAddressPoolService.Create не валидирует `name` — informational

- **Severity**: cosmetic / informational (не bug)
- **Found by**: `IPL-CR-VAL-MISSING-NAME` (internal-pool)
- **Status**: open, informational
- **Service**: kacho-vpc · **Method**: `InternalAddressPoolService.Create`
- **Symptoms**: `POST /vpc/v1/addressPools` без `name` → 200, pool с `name=""`.
  Валидируются только `kind` (≠ unspecified) и `cidrBlocks` (≥1, IPv4, host-bits=0).
- **Comment**: AddressPool — kacho-only admin resource (verbatim-YC аналога нет),
  unnamed pool технически валиден (ResolvePool работает по id/labels, не по name).
  Если потом добавят `NameVPC`-валидацию — кейс ассертит `oneOf([200,400])` и не сломается.
  Действий не требуется; зафиксировано для прозрачности.

### FINDING-008 — ExplainResolution / AllocateExternalIP на unresolvable input → code 13 INTERNAL — FIXED

- **Severity**: low (UX, не корректность)
- **Found by**: `IPL-EXPLAIN-UNRESOLVABLE` (internal-pool)
- **Status**: **fixed** (2026-05-11)
- **Service**: kacho-vpc · **Method**: `InternalAddressPoolService.ExplainResolution` (и `AllocateExternalIP`)
- **Symptoms (было)**: `GET /vpc/v1/addressPools:explainResolution?networkId=<unbound>` когда нет
  global-default pool (zone IS NULL) → `{"code":13,"message":"address pool admin error"}` —
  `doResolve` возвращал `service.ErrPoolNotResolved`, но `internalMapErr` его не классифицировал
  → default-ветка `codes.Internal` с masked-текстом.
- **Fix**: в `internal/handler/internal_maperr.go` добавлен `case errors.Is(err, service.ErrPoolNotResolved):
  return codes.FailedPrecondition` — теперь unresolvable → `9 FAILED_PRECONDITION` (sentinel-текст, без leak'а).
  Кейс `IPL-EXPLAIN-UNRESOLVABLE` ассертит `oneOf([9,5])` (не 13).

### FINDING-009 — InternalCloudService.SetPoolSelector не проверяет существование cloud_id — informational

- **Severity**: low
- **Found by**: `CLD-SEL-SET-UNKNOWN-CLOUD` (internal-cloud)
- **Status**: open, informational — proto-комментарий исправлен (2026-05-11)
- **Service**: kacho-vpc · **Method**: `InternalCloudService.SetPoolSelector` / `UnsetPoolSelector`
- **Symptoms**: `POST /vpc/v1/clouds/<nonexistent>/poolSelector` → 200; row создаётся в
  `cloud_pool_selector`. `AddressPoolService.SetCloudPoolSelector` делает только
  `cloud_id != ""`-проверку и `cloudSel.Set` (upsert) — без RM-вызова.
- **Действие**: proto-комментарий `internal_cloud_service.proto` исправлен — больше не утверждает,
  что cloud_id валидируется (кросс-DB FK нет; «висячий» selector безвреден — без живых folder→cloud
  он никогда не зарезолвится в cascade). Реальная валидация потребовала бы `CloudService.Exists` RPC
  на resource-manager — не делаем (cross-repo фича, dangling selector безопасен).
  Аналогично `GetPoolSelector` на unknown cloud → `present=false` (by design).

---

## Active bugs (severity > cosmetic)

_(пусто — на момент 2026-05-11; FINDING-005 и FINDING-008 закрыты; FINDING-007/009 — informational)_

---

## Closed

| ID | Severity | Closed | Fix |
|---|---|---|---|
| FINDING-005 — нет UNIQUE (folder, name) для 6 VPC-ресурсов | High | 2026-05-11 | migration `0002_resource_name_unique.sql` |

---

## Statistics

| Severity | Open | Fixed | Total |
|---|---|---|---|
| Critical | 0 | 0 | 0 |
| High | 0 | 1 | 1 |
| Medium | 0 | 0 | 0 |
| Low | 0 | 0 | 0 |
| Cosmetic | 0 | 0 | 0 |
| **Bugs total** | **0** | **1** | **1** |
| Findings (informational) | 5 | 2 | 7 (001–004 + 009 open; 005, 008 fixed) |
| Findings (invalid / test errors) | — | — | 1 (FINDING-006) |

---

## Анти-фидбэк (что **не** считать багом)

| Наблюдение | Почему не bug |
|---|---|
| Sync 404 на Update/Delete несуществующего | Intentional — AuthZ требует знания folder_id, см. FINDING-001 |
| `:add-cidr-blocks` vs `:addCidrBlocks` | Proto-decided REST mapping (kebab-case verbatim YC) |
| GetByValue → NotFound для cross-tenant IP | Intentional — info-leak prevention (TODO #50) |
| OpsProxy 400 для unknown prefix | Architectural choice — fail-fast routing, не bug. См. FINDING-003 |
| `RouteTable` enp-prefix вместо `rtb` | Architectural — все VPC ресурсы под `enp` для 3-char routing |
| PE Create с плоским `subnetId` "тихо успешен" | Test error — поля нет в proto; verbatim-форма `addressSpec.internalIpv4AddressSpec.subnetId` валидируется. См. FINDING-006 |
