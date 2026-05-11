# newman2 — финальный прогон (v6: ECP/BVA exhaustive)

**Дата:** 2026-05-11
**Окружение:** local (kind-кластер, port-forward 18080)

## Сводка прогона

| Сервис | Cases | Assertions | Failed | Requests | Status |
|---|---|---|---|---|---|
| network | 58 | 171 | 0 | 104 | ✅ |
| subnet | 62 | 390 | 0 | 264 | ✅ |
| address | 52 | 179 | 0 | 113 | ✅ |
| route-table | 45 | 187 | 0 | 125 | ✅ |
| security-group | 49 | 233 | 0 | 156 | ✅ |
| gateway | 44 | 111 | 0 | 68 | ✅ |
| private-endpoint | 31 | 129 | 0 | 98 | ✅ |
| operation | 4 | 16 | 0 | 7 | ✅ |
| **Итого** | **345** | **1416** | **0** | **935** | **100% PASS** |

## Coverage по applicable matrix

Все 8 классов на 100% покрытия по applicable RPC. См. v5 RESULTS history.

## Сопоставление с эталоном testing-product-coach §5.1

Эталон: ~100-200 кейсов на ресурс, для 7 публичных ресурсов ≈ 700-1400 cases.

| Ресурс | Текущее | Target lower | Target upper | % к нижней |
|---|---|---|---|---|
| network | 58 | 100 | 200 | 58% |
| subnet | 62 | 100 | 200 | 62% |
| address | 52 | 100 | 200 | 52% |
| route-table | 45 | 100 | 200 | 45% |
| security-group | 49 | 100 | 200 | 49% |
| gateway | 44 | 100 | 200 | 44% |
| private-endpoint | 31 | 100 | 200 | 31% |
| operation | 4 | (n/a — 1 RPC) | — | — |
| **Итого** | **341** | **700** | **1400** | **49%** |

## История прогонов

| Прогон | Cases | Assertions | Failed | Покрытие классов | На ресурс |
|---|---|---|---|---|---|
| v1 | 89 | 467 | 0 | 2/8 ≥80% | ~11 |
| v2 | 133 | 687 | 0 | 4/8 ≥80% | ~17 |
| v3 | 174 | 824 | 0 | 6/8 ≥80% | ~22 |
| v4 | 188 | 862 | 0 | 8/8 ≥80% | ~24 |
| v5 | 221 | 1047 | 0 | 8/8 100% applicable | ~28 |
| **v6** | **345** | **1416** | **0** | **8/8 100%** + ECP/BVA exhaustive | **~43** |

## Что добавлено в v6 (ECP/BVA exhaustive)

| Helper | Cases × ресурс | Содержание |
|---|---|---|
| `ecp_name_block` | 7 | empty/63/64/uppercase/digit-start/hyphen-start/special-chars |
| `ecp_description_block` | 2 | max 256 / over 257 |
| `ecp_labels_block` | 4 | uppercase key / invalid char / max 64 / over 65 |
| `updatemask_decision_table` | 2 | empty mask / multiple-unknown |
| `filter_syntax_block` | 3 | name="foo" / garbage / unknown field |
| `pagination_roundtrip` | 1 | page1 → token → page2 |
| `idempotency_block` | 1 | повторный Create same input |

Итого ~20 helper-кейсов × 7 ресурсов = ~140 новых cases.

## Gap до эталона (~360 cases)

| Зона | Cases needed | Реализация |
|---|---|---|
| Cross-resource validation | ~5/ресурс = 35 | Subnet с network из другого folder, Address с subnet из другой network |
| Exhaustive CIDR boundary | ~10 для Subnet | /0, /1, /31, /32, /28, /27, edge prefixes |
| Multi-CIDR AddCidrBlocks BVA | ~5 для Subnet | 1, 2, 5, 10 blocks, overlap matrix |
| Verbatim text snapshots для каждого error | ~10/ресурс = 70 | byte-level snapshot каждой формулировки ошибки |
| Concurrency tests (parallel Create) | ~3/ресурс = 21 | через Postman Collection Runner --iteration parallel |
| AlreadyExists matrix | ~3/ресурс = 21 | UNIQUE constraint coverage |
| Move circular / same-folder | ~2/ресурс = 14 | Move в тот же folder, обратно |
| Status transition (SG status, etc) | ~5/ресурс = 35 | для ресурсов со state machine |
| RBAC matrix (с двумя caller contexts) | ~10/ресурс = 70 | требует production AuthMode + два набора headers |
| Update happy для каждого mutable field | ~5/ресурс = 35 | name / description / labels отдельно |
| Performance baseline asserts | ~3/ресурс = 21 | response time < N ms |
| Pagination через iteration runner | ~3/ресурс = 21 | Collection Runner data file |

Итого backlog ≈ +360 cases для достижения 100/ресурс (нижняя граница эталона).

## Что недостающее закрывает (rationale)

| Не покрытое | Тип риска | Severity |
|---|---|---|
| RBAC matrix | Security (cross-tenant) | P0 |
| AlreadyExists detection | Data integrity | P1 |
| Concurrency parallel-Create | Race conditions | P0 |
| Verbatim text byte-level | Conformance with reference | P1 |
| Performance assert | Latency regression | P2 |
| CIDR boundary edge | Subnet allocator correctness | P1 |

## Команды

```bash
cd newman2 && ./scripts/run.sh           # все 8 коллекций
./scripts/run.sh --service subnet         # одна
python3 scripts/gen.py                    # регенерация из cases/*.py
```

## Достигнутое состояние

| Метрика | Значение |
|---|---|
| Pass rate | 100% (1416/1416) |
| API surface coverage | 60/60 RPC (100%) |
| Coverage по 8 classes | 100% applicable per class |
| Кейсов на ресурс (среднее) | ~43 |
| До target 100/ресурс | 49% |
| До target 200/ресурс | 25% |

Сьюта стабильна, формально полная (всё applicable покрыто хотя бы одним
кейсом), но плотность кейсов внутри классов составляет половину эталона
testing-product-coach. Дальнейшее наращивание — backlog v7.
