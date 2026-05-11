# newman2 — финальный прогон (v5, 100% coverage)

**Дата:** 2026-05-11
**Окружение:** local (kind-кластер, port-forward 18080)

## Сводка прогона

| Сервис | Cases | Assertions | Failed | Requests | Status |
|---|---|---|---|---|---|
| network | 38 | 139 | 0 | 81 | ✅ |
| subnet | 43 | 309 | 0 | 205 | ✅ |
| address | 33 | 150 | 0 | 93 | ✅ |
| route-table | 26 | 106 | 0 | 66 | ✅ |
| security-group | 30 | 152 | 0 | 97 | ✅ |
| gateway | 25 | 82 | 0 | 48 | ✅ |
| private-endpoint | 22 | 93 | 0 | 67 | ✅ |
| operation | 4 | 16 | 0 | 7 | ✅ |
| **Итого** | **221** | **1047** | **0** | **664** | **100% PASS** |

## Coverage по классам (applicable matrix)

| Класс | Applicable RPC | Covered | % | Status |
|---|---|---|---|---|
| CRUD | 60 | 60 | **100%** | 🎯 |
| NEG | 60 | 60 | **100%** | 🎯 |
| VAL | 25 | 25 | **100%** | 🎯 |
| AUTHZ | 22 | 22 | **100%** | 🎯 |
| BVA | 7 | 7 | **100%** | 🎯 |
| PAGE | 7 | 7 | **100%** | 🎯 |
| STATE | 10 | 10 | **100%** | 🎯 |
| CONF | 36 | 36 | **100%** | 🎯 |

**8/8 классов на 100%.** 60/60 RPC покрыто.

## Applicable matrix (объяснение)

| Класс | К каким RPC применим | RPC |
|---|---|---|
| CRUD | Все 60 — happy path для каждого | 60 |
| NEG | Все 60 — negative scenario | 60 |
| VAL | Create / Update / Move / Add/RemoveCidr / Relocate / UpdateRules / UpdateRule | 25 |
| AUTHZ | Update / Delete / Move / UpdateRules / UpdateRule (sync-Get RPC) | 22 |
| BVA | Верхнеуровневые List (folder_id + pageSize) | 7 |
| PAGE | Верхнеуровневые List | 7 |
| STATE | Update / AddCidr / RemoveCidr / Relocate | 10 |
| CONF | Get / Create / Move / Relocate / Delete / Update — verbatim text | 36 |

## История прогонов

| Прогон | Cases | Assertions | Failed | Классы ≥80% |
|---|---|---|---|---|
| v1 (initial) | 89 | 467 | 0 | 2/8 |
| v2 (+ BVA/CONF/STATE/Move) | 133 | 687 | 0 | 4/8 |
| v3 (+ STATE/CONF) | 174 | 824 | 0 | 6/8 |
| v4 (+ CONF-Move-NF) | 188 | 862 | 0 | 8/8 (CRUD 82%, CONF 86%) |
| **v5 (final)** | **221** | **1047** | **0** | **8/8 на 100%** 🎯 |

## Findings (BUG-MAP.md)

| ID | Severity | Status |
|---|---|---|
| FINDING-001 | documentation-gap | triaged (intentional) |
| FINDING-002 | documentation-gap | documented |
| FINDING-003 | documentation-gap | triaged |
| FINDING-004 | cosmetic | confirmed-intentional |
| **FINDING-005** | **P0** | **open** — Subnet не имеет UNIQUE constraint на (folder_id, name) |

## Команды

```bash
cd newman2 && ./scripts/run.sh           # все 8 коллекций
./scripts/run.sh --service subnet         # одна
python3 scripts/gen.py                    # регенерация из cases/*.py
```

## Что протестировано — итог

| Слой проверки | Покрытие |
|---|---|
| Happy CRUD lifecycle | 60/60 RPC |
| Negative path (NotFound, AlreadyExists, FailedPrecondition) | 60/60 RPC |
| Required-field validation | 25/25 mutation RPC |
| Cross-tenant AuthZ (sync-NF protection) | 22/22 sync-Get RPC |
| Pagination boundary (BVA + page_token) | 7/7 toplevel Lists |
| State machine (immutable fields, transitions) | 10/10 stateful RPC |
| Verbatim YC text conformance | 36/36 user-visible RPC |
