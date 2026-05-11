# Requirements — newman2

Требования / улучшения, выведенные из тестового анализа. Не баги —
предложения по уточнению контракта, добавлению функциональности,
улучшению тестируемости. См. формат в шаблоне.

---

## Active requirements

### REQ-001 — Pre-seeded zones + default-pool fixtures для testbed

- **Type**: testability
- **Priority**: P0
- **Driver**: Все Subnet/Address mutation-кейсы
- **Description**: kacho-deploy init-job должен seed'ить регион `ru-central1`
  + zones `ru-central1-{a,b,c,d}` + default `AddressPool` на zone `a`
  для `EXTERNAL_PUBLIC` (например 198.51.100.0/24). Сейчас приходится seed'ить
  вручную через `kachoctl-ipam` или curl на каждом `make dev-up`.
- **Rationale**: без правильных fixtures suite падает массово на sync-валидации
  `zone_id` и на Allocate external IP. Невозможно reproducible CI.
- **Impact**: 100% suite автоматически зелёный после `make dev-up`.
- **Owner**: `kacho-deploy` (Helm post-install Job).

### REQ-002 — Pre-seeded folders с детерминированными ID

- **Type**: testability
- **Priority**: P0
- **Driver**: Все mutation-кейсы (NET-CR-CRUD-OK, SUB-CR, ...)
- **Description**: kacho-deploy init-job создаёт два Folder с фиксированными
  ID или экспортирует actual IDs в ConfigMap. Newman читает env из ConfigMap
  при старте.
- **Rationale**: после каждого `make dev-up` `local.postman_environment.json`
  устаревает — IDs новых folders случайны, env приходится править руками.
- **Impact**: zero-touch repeatable runs.
- **Owner**: `kacho-deploy` + `newman2/scripts/`.

### REQ-003 — Документ REST endpoints map

- **Type**: documentation
- **Priority**: P1
- **Driver**: FINDING-002 — 15 case-developments тратили время на угадывание путей
- **Description**: Единая таблица в `kacho-vpc/docs/architecture/04-api-surface.md`
  с полным списком REST endpoint'ов (HTTP method + path + RPC + параметры).
  Сейчас приходится читать proto-файлы для каждого RPC.
- **Rationale**: developer experience + onboarding нового тестировщика.
  Сейчас неочевидно: `:add-cidr-blocks` (kebab) vs `:move` (single word) vs
  `/operations` (без /vpc/v1/) vs `/endpoints` (вместо `/privateEndpoints`).
- **Impact**: время на новый RPC test case падает с ~10 мин до ~2 мин.
- **Owner**: `kacho-vpc/docs/architecture/`.

### REQ-004 — Нормализовать OperationService.Get на 404 для unknown prefix

- **Type**: contract-clarification
- **Priority**: P2
- **Driver**: FINDING-003
- **Description**: `GET /operations/garbage-id` сейчас возвращает 400 InvalidArgument
  "operation_id has unknown prefix". Это противоречит resource-Get convention
  ("garbage id → 404 NotFound"). Рассмотреть один из вариантов:
  - **A**: OpsProxy конвертирует unknown-prefix → 404 NOT_FOUND `"Operation X not found"`.
  - **B**: Документировать как известное расхождение в `docs/architecture/06-conventions.md`.
- **Rationale**: предсказуемость для клиентов (`yc` CLI ожидает NOT_FOUND).
- **Impact**: меньше user confusion.
- **Owner**: `kacho-api-gateway/internal/opsproxy/`.

### REQ-005 — exhaustive UpdateMask test matrix per Update RPC

- **Type**: testability
- **Priority**: P1
- **Driver**: Только Network и Subnet имеют один STATE-кейс immutable
- **Description**: Для каждого Update RPC (8 ресурсов) добавить decision-table
  из 4 классов: empty-mask, unknown-field, immutable-field, mutable-field-OK.
  Это ~32 кейса.
- **Rationale**: UpdateMask — критичная точка контракта (verbatim YC), сейчас
  слабо покрыта. См. TESTING-PRODUCT §3.3 (decision tables).
- **Impact**: вылавливание регрессий verbatim-parity при изменении handler-ов.
- **Owner**: `newman2/cases/` v2.

### REQ-006 — Cross-tenant AuthZ test matrix

- **Type**: missing-feature (test)
- **Priority**: P0 (security)
- **Driver**: TESTING-PRODUCT §11.4 — критичное покрытие
- **Description**: Прогон с разными `x-kacho-folder-id` headers. Caller с
  folder=A пытается Get/Update/Delete ресурс в folder=B → PERMISSION_DENIED.
  Сейчас все кейсы используют anonymous (= admin в dev). Реально cross-tenant
  не проверен.
- **Rationale**: критическая security проверка перед IAM merge.
- **Impact**: гарантия отсутствия cross-tenant data leak.
- **Owner**: `newman2/cases/` v2 + setup environment с двумя header sets.

### REQ-007 — Concurrency invariant tests (allocator race, parallel Create)

- **Type**: missing-feature (test)
- **Priority**: P0
- **Driver**: TESTING-PRODUCT §3.10 — property-based для аллокатора
- **Description**: Newman + параллельные запросы (через cell или batch):
  - 10 одновременных `Create` с одинаковым именем → ровно 1 успех, 9 AlreadyExists.
  - 10 одновременных `AllocateExternalIP` → все 10 уникальны IP (no race).
- **Rationale**: race-free constraint — критичный инвариант.
- **Impact**: ловля concurrency-регрессий до prod.
- **Owner**: `newman2/cases/concurrency.py` (новый file).

### REQ-008 — Differential conformance suite (--env yc)

- **Type**: missing-feature
- **Priority**: P0 для production cut
- **Driver**: TESTING-PRODUCT §11.5 — verbatim YC parity
- **Description**: newman2 env-файл `yc.postman_environment.json` + run.sh
  flag `--env yc`. Все CRUD/NEG кейсы прогоняются и в kacho, и в YC,
  responses сравниваются byte-level. Расхождения → PARITY.md (pending или
  kacho-only).
- **Rationale**: единственный способ гарантировать verbatim-parity.
- **Impact**: high-confidence release readiness.
- **Owner**: `newman2/` v2.

---

## Backlog (P2/P3)

### REQ-009 — Performance baseline budget

- **Type**: observability
- **Priority**: P2
- **Description**: добавить assert на response time (`pm.response.responseTime < 500`)
  для Get/List. Это даёт регрессионный gate на perf.

### REQ-010 — Newman в CI

- **Type**: dx
- **Priority**: P2
- **Description**: GitHub Actions job который deploy'ит kind + Postgres + сервисы,
  делает port-forward на 18080, прогоняет `newman2/scripts/run.sh`. Сейчас
  newman только локально.

---

## Closed (реализовано)

_(пусто)_
