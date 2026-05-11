# newman — публичный API kacho-vpc, 100% coverage suite

Параллельная регрессионная сьюта, спроектированная по `testing-product-coach`
(black-box, формальные техники test design) с naming/structure по
`testing-code-coach`. Цель — покрыть **все публичные RPC** kacho-vpc
систематически, с явной taxonomy, prioritization и tracking-документами.

> Существующий `newman/` остаётся как baseline. `newman/` — отдельная
> ветка, не пересекается с master collection (`kacho-vpc.postman_collection.json`).

## Структура

```
newman/
├── README.md                — этот файл
├── collections/             — Postman-коллекции по сервису
│   ├── network.postman_collection.json
│   ├── subnet.postman_collection.json
│   ├── address.postman_collection.json
│   ├── route-table.postman_collection.json
│   ├── security-group.postman_collection.json
│   ├── gateway.postman_collection.json
│   ├── private-endpoint.postman_collection.json
│   └── operation.postman_collection.json
├── environments/
│   └── local.postman_environment.json
├── scripts/
│   ├── run.sh               — прогон одного / всех сервисов
│   └── report.py            — агрегация результатов
├── docs/
│   ├── TAXONOMY.md          — классы кейсов и naming convention
│   ├── TEST-PLAN.md         — карта покрытия (RPC × class)
│   ├── BUG-MAP.md           — найденные дефекты
│   ├── REQUIREMENTS.md      — требования к продукту из тестового анализа
│   └── RESULTS.md           — последний прогон pass/fail
└── out/                     — newman raw output (gitignored)
```

## Быстрый старт

```bash
# 1. Поднять стенд + port-forward на 18080 (см. kacho-deploy)
# 2. Прогнать всё
./scripts/run.sh

# Только один сервис
./scripts/run.sh --service network

# Только один класс кейсов (по тегу)
./scripts/run.sh --class NEG
```

## Принципы (из testing-product-coach)

- **Black-box**: тестируем продукт через публичный gRPC/REST, не код.
  Тест не должен знать о SQLSTATE, имени constraint'а, конкретной БД.
- **Источник истины**: acceptance-spec + proto-определения + reference YC.
- **Изоляция**: каждый case-сценарий внутри своего runId; suite внутри
  pre-allocated `existingFolderId` (env).
- **Формальные техники**: ECP, BVA, decision tables, state transition,
  error guessing — все классы кейсов выводятся системно.
- **Conformance**: каждый кейс должен иметь зеркало против YC (через
  `--env yc` — не реализовано в newman v1, см. TEST-PLAN).
- **Risk-prioritization**: high-risk зоны (AuthZ, allocator,
  data-integrity) получают больше кейсов.

См. подробности в `docs/TAXONOMY.md`.
