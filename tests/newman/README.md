# tests/newman — публичный API kacho-vpc, 100% coverage suite

**Главная regression-инфраструктура** kacho-vpc (`tests/newman/`; рядом `tests/k6/` —
нагрузочные сценарии). Black-box покрытие всех публичных RPC с формальными техниками
test design (ECP / BVA / decision tables / state transition / pairwise / error guessing)
и единым naming/structure. Источник истины — декларативные case-файлы `cases/*.py`;
коллекции в `collections/` **генерируются** скриптом `scripts/gen.py`.

## Структура

```
tests/newman/
├── README.md                — этот файл
├── cases/                   — ИСТОЧНИК ИСТИНЫ: декларативные case-наборы (Python), по сервису
│   ├── {network,subnet,address,route-table,security-group,gateway,operation}.py  — публичные RPC
│   └── {internal-pool,internal-cloud}.py  — internal/admin IPAM RPC (kacho-only)
├── collections/             — СГЕНЕРИРОВАННЫЕ Postman-коллекции (по сервису) — НЕ править руками
│   └── {…}.postman_collection.json
├── environments/
│   └── local.postman_environment.json   — local stand (port-forward api-gateway → 18080)
├── scripts/
│   ├── gen.py                — генератор коллекций из cases/* (Postman v2.1 JSON)
│   ├── run.sh                — прогон одного/всех сервисов целиком (newman + JSON reporter → out/)
│   ├── run-incremental.sh    — прогон ПО ОДНОМУ кейсу за раз + зачистка ресурсов после каждого (низкий resource-footprint); --resume / --cleanup-only
│   └── run-incremental.js    — драйвер (newman library API — без per-case process startup; env SERVICES=... ограничивает список сервисов)
├── docs/
│   ├── TAXONOMY.md            — классы кейсов и naming convention
│   ├── TEST-PLAN.md           — карта покрытия (RPC × класс)
│   ├── CASES-INDEX.md         — каталог уникальных паттернов кейсов
│   ├── PRODUCT-REQUIREMENTS.md — НОРМАТИВНЫЙ регламент REQ-* (выведен из CASES-INDEX; источник для conformance-проверок)
│   ├── REQUIREMENTS.md        — бэклог *улучшений* (testability / contract-clarification asks — не нормативный)
│   └── RESULTS.md             — последний прогон pass/fail + история версий
└── out/                     — newman raw output + summary.txt (gitignored snap-логи)
```
(Найденные дефекты/наблюдения — в issue-трекере репозитория; намеренные особенности
контракта — `docs/architecture/07-known-divergences.md`.)

## Быстрый старт

```bash
# 1. Поднять стенд + port-forward api-gateway → localhost:18080 (см. kacho-deploy)
# 2. Перегенерить коллекции из cases/*.py (если меняли cases или код)
python3 scripts/gen.py            # все сервисы; или: python3 scripts/gen.py network
# 3a. Прогнать все одним махом (быстро, но во время прогона создается много ресурсов разом)
./scripts/run.sh                  # сводка в out/summary.txt
./scripts/run.sh --service network                 # один сервис
# 3b. Прогнать ПО ОДНОМУ кейсу за раз с зачисткой ресурсов после каждого
#     (низкий resource-footprint в любой момент)
./scripts/run-incremental.sh                        # все ~731 кейс; сводка → out/incremental/summary.txt
./scripts/run-incremental.sh --resume               # продолжить прерванный прогон
./scripts/run-incremental.sh --service subnet       # один сервис
./scripts/run-incremental.sh --cleanup-only         # просто стереть throwaway-ресурсы в тест-папках
#     тюнинг через env: CLEANUP_EVERY (как часто periodic-cleanup, default 25), DELAY_REQUEST (ms, default 30), SERVICES='svc1 svc2 ...'

# Требует KACHO_VPC_DEFAULT_SG_INLINE=true (default) — иначе кейсы default-SG краснеют.
#   результат → out/incremental/{progress.tsv, summary.txt, failed/<id>.json}.
```

## Принципы

- **Black-box**: тестируем продукт через публичный gRPC/REST, не код.
  Тест не должен знать о SQLSTATE, имени constraint'а, конкретной БД.
- **Источник истины**: acceptance-spec + proto-определения + контракт Kachō.
- **Изоляция**: каждый case-сценарий внутри своего runId; suite внутри
  pre-allocated `existingProjectId` (env).
- **Формальные техники**: ECP, BVA, decision tables, state transition,
  error guessing — все классы кейсов выводятся системно.
- **Conformance**: кейсы фиксируют контракт Kachō (тексты ошибок, коды, форматы).
- **Risk-prioritization**: high-risk зоны (AuthZ, allocator,
  data-integrity) получают больше кейсов.

См. подробности в `docs/TAXONOMY.md`.
