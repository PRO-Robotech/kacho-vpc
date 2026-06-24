# Taxonomy кейсов newman

## Naming convention

```
<DOMAIN>-<METHOD>-<CLASS>-<DETAIL>
```

| Часть | Пример |
|---|---|
| DOMAIN | `NET` (Network), `SUB` (Subnet), `ADR` (Address), `RT` (RouteTable), `SG` (SecurityGroup), `GW` (Gateway), `OP` (Operation) |
| METHOD | `CR` (Create), `GET`, `LST` (List), `UPD` (Update), `DEL` (Delete), `MV` (Move), `ACB` (AddCidrBlocks), `RCB` (RemoveCidrBlocks), `REL` (Relocate), `URL` (UpdateRules), `UR` (UpdateRule), `LUA` (ListUsedAddresses), `GBV` (GetByValue), `LBS` (ListBySubnet), `LOP` (ListOperations) |
| CLASS | `CRUD`, `VAL`, `NEG`, `BVA`, `IDM`, `CONC`, `CONF`, `STATE`, `AUTHZ`, `PAGE`, `FILTER` |
| DETAIL | свободное краткое описание |

Примеры: `NET-CR-CRUD-OK`, `SUB-CR-VAL-CIDR-HOSTBITS`,
`ADR-GBV-NEG-NOT-FOUND`, `NET-LST-BVA-PAGESIZE-0`.

## Классы

| Класс | Назначение | Техника источника |
|---|---|---|
| `CRUD` | Happy path: создать → прочитать → обновить → удалить | Use-case |
| `VAL` | Sync-валидация полей: required, format, regex, length | ECP + BVA по полю |
| `NEG` | Async-ошибки: NotFound, PermissionDenied, FailedPrecondition, ResourceExhausted | Decision tables |
| `BVA` | Boundary values на числовых/строковых параметрах | BVA |
| `IDM` | Идемпотентность retry-safe операций | Property-based |
| `CONC` | Concurrency invariants (parallel создание same name, race-free allocator) | Stress + invariant |
| `CONF` | Conformance к контракту Kachō: текст ошибки, формат response | Approval / snapshot |
| `STATE` | State transition / immutable fields | State machine testing |
| `AUTHZ` | Cross-tenant access denial | Permission matrix |
| `PAGE` | Pagination boundary + token roundtrip | BVA + property |
| `FILTER` | Filter syntax + supported fields | ECP по filter expression |

## Применение по методам

| Метод RPC | Обязательные классы | Доп. |
|---|---|---|
| `Get<Resource>` | CRUD, NEG (NotFound), AUTHZ, CONF | — |
| `List<Resource>s` | CRUD, NEG, AUTHZ, PAGE, FILTER, BVA (page_size) | — |
| `Create<Resource>` | CRUD, VAL (all required + format), NEG (parent NotFound, duplicate name), AUTHZ, CONF | IDM (если detect-able), CONC (race на same name) |
| `Update<Resource>` | CRUD, VAL (mask + immutable), NEG, AUTHZ, STATE (immutable fields reject) | — |
| `Delete<Resource>` | CRUD, NEG (NotFound + deletion_protection + has-children), AUTHZ, CONF | STATE (Delete во время Create/Update — если detect-able) |
| `Move<Resource>` | CRUD, NEG (destination not found, cross-cloud), AUTHZ, STATE | — |
| `<Resource>.ListOperations` | CRUD, NEG, AUTHZ, PAGE, FILTER | — |
| `Subnet.AddCidrBlocks` | CRUD, VAL (host-bits), NEG (overlap), CONF | — |
| `Subnet.RemoveCidrBlocks` | CRUD, VAL, NEG (cannot remove last), STATE | — |
| `Subnet.Relocate` | CRUD, NEG (has Addresses), CONF | STATE |
| `Subnet.ListUsedAddresses` | CRUD, NEG, PAGE | — |
| `Address.GetByValue` | CRUD, NEG (not found = denied для info-leak), AUTHZ | — |
| `Address.ListBySubnet` | CRUD, NEG, PAGE | — |
| `SecurityGroup.UpdateRules` | CRUD, VAL (rule format), CONC (xmin OCC), STATE | — |
| `SecurityGroup.UpdateRule` | CRUD, VAL, NEG (rule not found), STATE | — |

## Priority уровни

| Priority | Применение |
|---|---|
| P0 | Security (AUTHZ), data-integrity (FK, EXCLUDE), allocator (CONC) — must-pass |
| P1 | CRUD happy, validation P0-полей (project_id, network_id, CIDR), conformance к контракту | 
| P2 | BVA, pagination, ECP полей с низким impact | 
| P3 | Cosmetic (labels, description), редкие state transitions |

В Newman tag'и используются для filtering: `class:CRUD`, `priority:P0`,
`domain:NET`.

## Что НЕ покрываем в newman (явный scope-cut)

| Зона | Причина | Альтернатива |
|---|---|---|
| Internal RPC (`:9091`) | Не публичный API | Отдельная suite `internal/` (вне scope newman) |
| Performance / load | Не функциональная проверка | Отдельный k6 setup |
| UI behaviour | Backend test, не frontend | `kacho-ui` E2E |
| Migration up/down | Operational, не product | `kacho-deploy` smoke |
| Disaster recovery | Operational | Quarterly drill |

## Test data lifecycle

| Уровень | Подход |
|---|---|
| Per-run | `runId = Date.now()+random.slice(-6)` в имени каждого ресурса |
| Per-suite | `_suiteProjectId` из env (pre-allocated в стенде) |
| Per-case | Project cleanup полагается на `Delete<Resource>` в кейсе |
| Cross-project | `_suiteProjectCrossId` для Move-кейсов |
