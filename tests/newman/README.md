# tests/newman — публичный API kacho-vpc, 100% coverage suite

**Главная regression-инфраструктура** kacho-vpc (`tests/newman/`; рядом `tests/k6/` —
нагрузочные сценарии). Black-box покрытие всех публичных RPC, спроектирована по
`testing-product-coach` (формальные техники test design) с naming/structure по
`testing-code-coach`. Источник истины — декларативные case-файлы `cases/*.py`;
коллекции в `collections/` **генерируются** скриптом `scripts/gen.py`.

> Старая quota-aware 3-suite сьюта против реального YC API (`newman_legacy/`,
> RO/LIGHT/SEQ, master collection) удалена — история в git.

## Структура

```
tests/newman/
├── README.md                — этот файл
├── cases/                   — ИСТОЧНИК ИСТИНЫ: декларативные case-наборы (Python), по сервису
│   ├── {network,subnet,address,route-table,security-group,gateway,private-endpoint,operation}.py  — публичные RPC
│   └── {internal-pool,internal-region-zone,internal-cloud}.py  — internal/admin IPAM RPC (kacho-only)
├── collections/             — СГЕНЕРИРОВАННЫЕ Postman-коллекции (по сервису) — НЕ править руками
│   └── {…}.postman_collection.json
├── environments/
│   └── local.postman_environment.json   — local stand (port-forward api-gateway → 18080)
├── scripts/
│   ├── gen.py               — генератор коллекций из cases/* (Postman v2.1 JSON)
│   └── run.sh               — прогон одного/всех сервисов (newman + JSON reporter → out/)
├── docs/
│   ├── TAXONOMY.md          — классы кейсов и naming convention
│   ├── TEST-PLAN.md         — карта покрытия (RPC × класс)
│   ├── CASES-INDEX.md       — каталог уникальных паттернов кейсов
│   ├── REQUIREMENTS.md      — требования к продукту из тестового анализа
│   └── RESULTS.md           — последний прогон pass/fail + история версий + skill-mapping
└── out/                     — newman raw output + summary.txt (gitignored snap-логи)
```
(Найденные дефекты/наблюдения — в GitHub Issues `PRO-Robotech/kacho-vpc`, см. `kacho-vpc/CLAUDE.md` §14.4;
by-design расхождения с verbatim YC — `docs/architecture/07-known-divergences.md`. Отдельного bug-map больше нет.)

## Быстрый старт

```bash
# 1. Поднять стенд + port-forward api-gateway → localhost:18080 (см. kacho-deploy)
# 2. Перегенерить коллекции из cases/*.py (если меняли cases или код)
python3 scripts/gen.py            # все сервисы; или: python3 scripts/gen.py network
# 3. Прогнать всё
./scripts/run.sh                  # сводка в out/summary.txt
# Один сервис / с задержкой / fail-fast
./scripts/run.sh --service network
./scripts/run.sh --service subnet --delay 60 --bail

# Требует KACHO_VPC_DEFAULT_SG_INLINE=true (default) — иначе кейсы default-SG краснеют.
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
