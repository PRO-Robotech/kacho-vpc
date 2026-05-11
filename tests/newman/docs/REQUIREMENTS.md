# Requirements — newman (бэклог улучшений)

> ⚠️ Это **не** нормативный регламент. Нормативные продуктовые требования (`REQ-*`, на
> соответствие которым проверяет агент-аудитор) — в `PRODUCT-REQUIREMENTS.md`. Здесь —
> **бэклог улучшений**: предложения по уточнению контракта, добавлению функциональности,
> улучшению тестируемости. Не баги (баги → GitHub Issues, см. `CLAUDE.md` §14.4).

---

## Active requirements

### REQ-001 — Pre-seeded zones + default-pool fixtures для testbed

- **Type**: testability
- **Priority**: P0
- **Driver**: Все Subnet/Address mutation-кейсы
- **Description**: kacho-deploy init-job должен seed'ить регион `ru-central1`
  + zones `ru-central1-{a,b,c,d}` + default `AddressPool` на zone `a`
  для `EXTERNAL_PUBLIC` (например 198.51.100.0/24). Сейчас приходится seed'ить
  вручную через curl на api-gateway на каждом `make dev-up`.
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
- **Owner**: `kacho-deploy` + `tests/newman/scripts/`.

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
- **Owner**: `tests/newman/cases/` v2.

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
- **Owner**: `tests/newman/cases/` v2 + setup environment с двумя header sets.

### REQ-007 — Concurrency invariant tests (allocator race, parallel Create)

- **Type**: missing-feature (test)
- **Priority**: P0
- **Driver**: TESTING-PRODUCT §3.10 — property-based для аллокатора
- **Description**: Newman + параллельные запросы (через cell или batch):
  - 10 одновременных `Create` с одинаковым именем → ровно 1 успех, 9 AlreadyExists.
  - 10 одновременных `AllocateExternalIP` → все 10 уникальны IP (no race).
- **Rationale**: race-free constraint — критичный инвариант.
- **Impact**: ловля concurrency-регрессий до prod.
- **Owner**: `tests/newman/cases/concurrency.py` (новый file).

### REQ-008 — Differential conformance suite против реального YC — `partial`

- **Type**: missing-feature
- **Priority**: P0 для production cut
- **Driver**: TESTING-PRODUCT §11.5 — verbatim YC parity
- **Сделано**: `environments/yc.postman_environment.json` + `scripts/yc-proxy.js` (локальный
  reverse-proxy: `/vpc/v1/*`→`vpc.api`, `/operations/*`→`operation.api`, подставляет Bearer
  `yc iam create-token`) → `ENV=environments/yc.postman_environment.json SERVICES='<8 public>'
  ./scripts/run-incremental.sh` гоняет сьюту против реального YC по одному кейсу за раз с
  зачисткой ресурсов. Упавшие кейсы = расхождения kacho↔YC (политика: всё, что ≠ YC, — баг → GitHub Issue).
- **Осталось**: (1) `internal-*` кейсы (45) тут не гоняются — в YC API этих RPC нет; (2) кейсы
  ассертят *наше* поведение → при расхождении надо и фиксить impl под YC, и переписывать ассерт
  кейса под YC-поведение; (3) автоматическая byte-level diff-классификация (сейчас — ручной разбор
  `out/incremental/failed/*.json`); (4) cross-folder Move-кейсы тут вырождаются (одна throwaway-folder).
- **Owner**: `tests/newman/`.

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
  делает port-forward на 18080, прогоняет `tests/newman/scripts/run.sh`. Сейчас
  newman только локально.

---

## Closed (реализовано)

_(пусто)_
