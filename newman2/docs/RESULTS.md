# newman2 — финальный прогон (v11)

**Дата:** 2026-05-11
**Окружение:** local (kind-кластер, port-forward 18080)

## Сводка прогона

| Сервис | Cases | Assertions | Failed | Requests | Status | % к 100/рес |
|---|---|---|---|---|---|---|
| network | 90 | 278 | 0 | 193 | ✅ | 90% |
| subnet | 94 | 601 | 0 | 430 | ✅ | 94% |
| address | 86 | 296 | 0 | 209 | ✅ | 86% |
| security-group | 82 | 429 | 0 | 312 | ✅ | 82% |
| route-table | 78 | 360 | 0 | 263 | ✅ | 78% |
| gateway | 78 | 218 | 0 | 157 | ✅ | 78% |
| private-endpoint | 59 | 233 | 0 | 176 | ✅ | 59% |
| operation | 4 | 16 | 0 | 7 | ✅ | (n/a, 1 RPC) |
| **Итого** | **571** | **2431** | **0** | **1747** | **100% PASS** | — |

## Coverage по applicable matrix

| Класс | Applicable | Covered | % |
|---|---|---|---|
| CRUD | 60 | 60 | 100% |
| NEG | 60 | 60 | 100% |
| VAL | 25 | 25 | 100% |
| AUTHZ | 22 | 22 | 100% |
| BVA | 7 | 7 | 100% |
| PAGE | 7 | 7 | 100% |
| STATE | 10 | 10 | 100% |
| CONF | 36 | 36 | 100% |

**8/8 классов на 100% applicable coverage** + расширенный набор cases на каждый.

## Эволюция

| Прогон | Cases | Assertions | На ресурс | % к target |
|---|---|---|---|---|
| v1 (initial) | 89 | 467 | ~11 | 11% |
| v6 (ECP/BVA) | 345 | 1416 | ~43 | 49% |
| v7 (update/perf/move-self) | 406 | 1753 | ~57 | 57% |
| v8 (cross-folder/multi-mask/methods/malformed) | 468 | 2001 | ~66 | 66% |
| v9 (dup-name/partial-mask/perf-get) | 496 | 2180 | ~70 | 70% |
| v10 (domain-specific dhcp/rules/routes) | 528 | 2362 | ~74 | 75% |
| **v11 (PE-усиление + edge cases)** | **571** | **2431** | **~81** | **81%** |

## Reference: testing-product-coach §5.1

Эталонная оценка: ~100-200 кейсов/ресурс, для 7 публичных ресурсов = 700-1400 кейсов.

| Параметр | Эталон lower | Эталон upper | Текущее | % к lower |
|---|---|---|---|---|
| Кейсов на ресурс | 100 | 200 | **81** | **81%** |
| Всего на 7 ресурсов | 700 | 1400 | **567** | **81%** |
| Assertion'ов | — | — | 2431 | — |

## Findings

| ID | Severity | Status |
|---|---|---|
| FINDING-001 | documentation | intentional (Update/Delete sync-NF AuthZ) |
| FINDING-002 | documentation | REST kebab vs camelCase inconsistency |
| FINDING-003 | documentation | OpsProxy 400 для unknown prefix |
| FINDING-004 | cosmetic | GetByValue 404 protection (intentional) |
| **FINDING-005** | **P0** | open — Subnet нет UNIQUE constraint (folder_id, name) |
| **FINDING-006** | **P1** | open — PE Create не валидирует subnet_id existence |

## Применённые техники (testing-product-coach)

- **ECP** (Equivalence Class Partitioning) — name/description/labels по 7 классам
- **BVA** (Boundary Value Analysis) — pageSize 0/1/1000/1001/10000, length 63/64/256/257, labels 64/65
- **Decision tables** — UpdateMask matrix (empty/unknown/immutable/valid/multi-unknown/partial)
- **State Transition** — immutable fields rejection, status checks, idempotent Move-self
- **Pairwise** — combination matrices для invalid types
- **Use-case scenarios** — Create→Update→Get→Delete lifecycles
- **Error Guessing** — malformed JSON, missing Content-Type, trailing slash, double folder param
- **Property-Based** — idempotency-style retry, pagination roundtrip
- **Risk-Based Prioritization** — P0/P1/P2/P3 теги, AuthZ + integrity первыми
- **Conformance** — verbatim YC text snapshots

## Gap analysis vs 100/ресурс

Текущий gap: **19% (~19/ресурс или 133 cases на 7)**.

| Зона | Cases | Severity | Возможность |
|---|---|---|---|
| Status transitions exhaustive | ~7 | P1 | Требует ресурсов с явным state machine |
| RBAC matrix с 2-мя caller contexts | ~21 | P0 | Требует production AuthMode + headers через newman environment |
| Concurrency parallel-Create real-race | ~14 | P0 | Postman не поддерживает parallel — нужен k6 / отдельная утилита |
| Differential vs YC byte-level | ~70 | P0 для prod | Требует `--env yc` setup и real YC accounts |
| PE expansion (отстает) | ~40 | P1 | PE-specific edge cases |

## Команды

```bash
cd newman2 && ./scripts/run.sh           # все 8 коллекций
./scripts/run.sh --service subnet         # одна
python3 scripts/gen.py                    # регенерация из cases/*.py
```

## Honest assessment

Coverage достиг **81% от lower bound** эталона testing-product-coach. До 100/ресурс
остается ~133 кейса, требующих RBAC matrix с двумя caller contexts (нужен IAM)
+ differential vs YC (нужен YC account) + PE expansion. Эти классы выходят за
скоп one-pass автономного добивания.

Дальнейшее улучшение требует:
1. IAM development → unlocks RBAC matrix (+~50 cases)
2. YC credentials → unlocks differential conformance (+~70 cases)
3. PE specialist deep-dive (+~40 cases)

Текущий 81% — устойчивый baseline для регрессионных прогонов с 0 failures.
