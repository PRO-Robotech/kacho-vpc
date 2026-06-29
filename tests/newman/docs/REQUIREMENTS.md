# Requirements — newman (бэклог улучшений)

> ⚠️ Это **не** нормативный регламент. Нормативные продуктовые требования (`REQ-*`, на
> соответствие которым проверяет агент-аудитор) — в `PRODUCT-REQUIREMENTS.md`. Здесь —
> **бэклог улучшений**: предложения по уточнению контракта, добавлению функциональности,
> улучшению тестируемости. Не баги (баги → issue-трекер репозитория).

---

## Active requirements

### REQ-001 — Pre-seeded zones + default-pool fixtures для testbed

- **Type**: testability
- **Priority**: P0
- **Driver**: Все Subnet/Address mutation-кейсы
- **Description**: kacho-deploy init-job должен seed'ить регион `zone`
  + zones `zone-{a,b,c,d}` + default `AddressPool` на zone `a`
  для `EXTERNAL_PUBLIC` (например 198.51.100.0/24). Сейчас приходится seed'ить
  вручную через curl на api-gateway на каждом `make dev-up`.
- **Rationale**: без правильных fixtures suite падает массово на sync-валидации
  `zone_id` и на Allocate external IP. Невозможно reproducible CI.
- **Impact**: 100% suite автоматически зеленый после `make dev-up`.
- **Owner**: `kacho-deploy` (Helm post-install Job).

### REQ-002 — Pre-seeded projects с детерминированными ID

- **Type**: testability
- **Priority**: P0
- **Driver**: Все mutation-кейсы (NET-CR-CRUD-OK, SUB-CR, ...)
- **Description**: kacho-deploy init-job создает два Project с фиксированными
  ID или экспортирует actual IDs в ConfigMap. Newman читает env из ConfigMap
  при старте.
- **Rationale**: после каждого `make dev-up` `local.postman_environment.json`
  устаревает — IDs новых projects случайны, env приходится править руками.
- **Impact**: zero-touch repeatable runs.
- **Owner**: `kacho-deploy` + `tests/newman/scripts/`.

### REQ-003 — Документ REST endpoints map

- **Type**: documentation
- **Priority**: P1
- **Driver**: case-разработка тратит время на угадывание REST-путей
- **Description**: Единая таблица в `kacho-vpc/docs/architecture/04-api-surface.md`
  с полным списком REST endpoint'ов (HTTP method + path + RPC + параметры).
  Сейчас приходится читать proto-файлы для каждого RPC.
- **Rationale**: developer experience + onboarding нового тестировщика.
  Сейчас неочевидно: `:add-cidr-blocks` (kebab) vs `:move` (single word) vs
  `/operations` (без /vpc/v1/).
- **Impact**: время на новый RPC test case падает с ~10 мин до ~2 мин.
- **Owner**: `kacho-vpc/docs/architecture/`.

### REQ-004 — Нормализовать OperationService.Get на 404 для unknown prefix

- **Type**: contract-clarification
- **Priority**: P2
- **Driver**: предсказуемость Get-конвенции для операций
- **Description**: `GET /operations/garbage-id` сейчас возвращает 400 InvalidArgument
  "operation_id has unknown prefix". Это противоречит resource-Get convention
  ("garbage id → 404 NotFound"). Рассмотреть один из вариантов:
  - **A**: OpsProxy конвертирует unknown-prefix → 404 NOT_FOUND `"Operation X not found"`.
  - **B**: Документировать как известное расхождение в `docs/architecture/06-conventions.md`.
- **Rationale**: предсказуемость для клиентов (resource-Get-конвенция: garbage id → NOT_FOUND).
- **Impact**: меньше user confusion.
- **Owner**: `kacho-api-gateway/internal/opsproxy/`.

### REQ-005 — exhaustive UpdateMask test matrix per Update RPC

- **Type**: testability
- **Priority**: P1
- **Driver**: Только Network и Subnet имеют один STATE-кейс immutable
- **Description**: Для каждого Update RPC (7 ресурсов) добавить decision-table
  из 4 классов: empty-mask, unknown-field, immutable-field, mutable-field-OK.
  Это ~28 кейсов.
- **Rationale**: UpdateMask — критичная точка контракта, сейчас
  слабо покрыта (decision tables).
- **Impact**: вылавливание регрессий контракта при изменении handler-ов.
- **Owner**: `tests/newman/cases/` v2.

### REQ-006 — Cross-tenant AuthZ test matrix

- **Type**: missing-feature (test)
- **Priority**: P0 (security)
- **Driver**: критичное security-покрытие
- **Description**: Прогон с разными `x-kacho-project-id` headers. Caller с
  project=A пытается Get/Update/Delete ресурс в project=B → PERMISSION_DENIED.
  Сейчас все кейсы используют anonymous (= admin в dev). Реально cross-tenant
  не проверен.
- **Rationale**: критическая security проверка перед IAM merge.
- **Impact**: гарантия отсутствия cross-tenant data leak.
- **Owner**: `tests/newman/cases/` v2 + setup environment с двумя header sets.

### REQ-007 — Concurrency invariant tests (allocator race, parallel Create)

- **Type**: missing-feature (test)
- **Priority**: P0
- **Driver**: property-based покрытие аллокатора
- **Description**: Newman + параллельные запросы (через cell или batch):
  - 10 одновременных `Create` с одинаковым именем → ровно 1 успех, 9 AlreadyExists.
  - 10 одновременных `AllocateExternalIP` → все 10 уникальны IP (no race).
- **Rationale**: race-free constraint — критичный инвариант.
- **Impact**: ловля concurrency-регрессий до prod.
- **Owner**: `tests/newman/cases/concurrency.py` (новый file).

### REQ-008 — Snapshot/differential conformance suite контракта — `backlog`

- **Type**: missing-feature
- **Priority**: P0 для production cut
- **Driver**: byte-level фиксация контракта (тексты ошибок / коды / форматы)
- **Description**: автоматическая snapshot-проверка ответов (статус, текст ошибки, форма
  response) против эталонного набора, зафиксированного по `PRODUCT-REQUIREMENTS.md`. Любое
  расхождение текста/кода с зафиксированным контрактом → красный кейс + блокирующее замечание.
- **Осталось**: (1) формат хранения эталонов (golden-файлы per case-id); (2) byte-level
  diff-классификация (текст vs код vs форма); (3) интеграция в `run-incremental.sh` как
  отдельный режим.
- **Owner**: `tests/newman/`.

---

## Backlog (P2/P3)

### REQ-009 — Performance baseline budget

- **Type**: observability
- **Priority**: P2
- **Description**: добавить assert на response time (`pm.response.responseTime < 500`)
  для Get/List. Это дает регрессионный gate на perf.

### REQ-010 — Newman в CI

- **Type**: dx
- **Priority**: P2
- **Description**: GitHub Actions job который deploy'ит kind + Postgres + сервисы,
  делает port-forward на 18080, прогоняет `tests/newman/scripts/run.sh`. Сейчас
  newman только локально.

---

## Closed (реализовано)

_(пусто)_
