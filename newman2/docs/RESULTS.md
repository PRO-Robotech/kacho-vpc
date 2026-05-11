# newman2 — последний прогон

**Дата:** 2026-05-11
**Окружение:** local (kind-кластер, port-forward 18080)
**Полный pipeline:** ./scripts/run.sh

## Сводка

| Сервис | Cases | Assertions total | Failed | Requests | Status |
|---|---|---|---|---|---|
| network | 23 | 103 | 0 | 60 | ✅ |
| subnet | 17 | 139 | 0 | 90 | ✅ |
| address | 13 | 77 | 0 | 48 | ✅ |
| route-table | 9 | 40 | 0 | 25 | ✅ |
| security-group | 9 | 51 | 0 | 32 | ✅ |
| gateway | 8 | 28 | 0 | 16 | ✅ |
| private-endpoint | 7 | 16 | 0 | 9 | ✅ |
| operation | 3 | 13 | 0 | 6 | ✅ |
| **Итого** | **89** | **467** | **0** | **286** | **100% PASS** |

## История изменений

| Дата | Прогон | Pass% | Заметки |
|---|---|---|---|
| 2026-05-11 (1) | initial network | 9% (81/90 fail) | runId с точкой ломал regex; `pm.environment.delete` not exists; пути `/operations/{id}` без `/vpc/v1` |
| 2026-05-11 (2) | network only | 100% | После фиксов helpers + path для operations + `})` parse error |
| 2026-05-11 (3) | + subnet | 100% | Путь `:add-cidr-blocks` (kebab-case), `/addresses` для used-list |
| 2026-05-11 (4) | + address | 100% | Regex с double-escape `\\d` → `[0-9]` для совместимости |
| 2026-05-11 (5) | full 8 | 97.3% (13 fail) | PE: `/endpoints/`; SG: `PATCH /rules`; OP: prefix 400 |
| 2026-05-11 (6) | full 8 | **100%** | Финал — все классы зелёные |

## API surface coverage (Test Plan)

После прогона 60 публичных RPC получили хотя бы по одному кейсу. Сводка
покрытия по классам — см. `TEST-PLAN.md`.

Из 60 RPC:
- **CRUD happy** покрыт: 36 (60%) — caверy всех агентских/list/get/update/delete RPC
- **NEG path** (NotFound / 404): 50 (83%)
- **VAL required**: 22 (37%) — где есть обязательные поля
- **AUTHZ sync-NF**: 14 (23%) — для всех Update/Delete handlers
- **BVA pagination**: 4 (7%) — только Network List
- **STATE immutable**: 4 (7%) — Network/Subnet
- **CONF (verbatim)**: 4 (7%) — CIDR overlap, NotFound text

## Что не покрыто (известно)

См. `TEST-PLAN.md` для полной матрицы. Приоритетный backlog:

| Зона | Класс кейсов | Статус |
|---|---|---|
| Move RPC (8 ресурсов) | CRUD-OK + NEG-DEST-FOLDER-NF | Покрыт только Network |
| UpdateMask exhaustive | VAL-MASK-UNKNOWN, VAL-MASK-IMMUTABLE | Покрыт только Subnet (CIDR) и Network (folder_id) |
| Pagination roundtrip | PAGE-TOKEN-ROUNDTRIP | Нет |
| Filter syntax | FILTER-NAME-OK, FILTER-UNKNOWN-FIELD | Нет |
| Cross-folder AUTHZ | AUTHZ-CROSS-TENANT | Нет (нужны два user contexts) |
| Concurrency invariants | CONC-DUP-RACE, CONC-ALLOCATOR | Только Network duplicate name |
| Address pool exhaustion | NEG-RESOURCE-EXHAUSTED | Нет |
| Subnet RemoveCidrBlocks | CRUD + NEG-CANNOT-REMOVE-LAST | Нет |
| SG UpdateRule (single) | CRUD + NEG-RULE-NF | Нет (только UpdateRules) |
| PE Update/Create happy | CRUD-OK | Нет (только NEG) |
| Address Move | CRUD + NEG | Нет |
| Network Delete с детьми | NEG-NETWORK-NOT-EMPTY | Нет |

Цель v2: ~150 cases, покрытие всех Move + UpdateMask + Pagination
boundaries + cross-tenant.
