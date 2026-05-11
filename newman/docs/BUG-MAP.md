# Bug Map — newman findings

Регистр **продуктовых** наблюдений из прогонов newman. На этом этапе
все 467 assertions зелёные — баги (нарушение спеки) **не найдены**, но
выявлен ряд расхождений с canonical YC pattern и documentation-gap'ов
(пограничные между "design choice" и "improvement opportunity"). Они
зафиксированы как FINDING-NNN (наблюдения без severity bug).

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

---

## Active bugs (severity > cosmetic)

_(пусто — на момент 2026-05-11)_

---

## Closed

_(пусто)_

---

## Statistics

| Severity | Open | Fixed | Total |
|---|---|---|---|
| Critical | 0 | 0 | 0 |
| High | 0 | 0 | 0 |
| Medium | 0 | 0 | 0 |
| Low | 0 | 0 | 0 |
| Cosmetic | 0 | 0 | 0 |
| **Bugs total** | **0** | **0** | **0** |
| Findings (informational) | 4 | — | 4 |

---

## Анти-фидбэк (что **не** считать багом)

| Наблюдение | Почему не bug |
|---|---|
| Sync 404 на Update/Delete несуществующего | Intentional — AuthZ требует знания folder_id, см. FINDING-001 |
| `:add-cidr-blocks` vs `:addCidrBlocks` | Proto-decided REST mapping (kebab-case verbatim YC) |
| GetByValue → NotFound для cross-tenant IP | Intentional — info-leak prevention (TODO #50) |
| OpsProxy 400 для unknown prefix | Architectural choice — fail-fast routing, не bug. См. FINDING-003 |
| `RouteTable` enp-prefix вместо `rtb` | Architectural — все VPC ресурсы под `enp` для 3-char routing |
