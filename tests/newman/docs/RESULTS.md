# newman — финальный прогон (v16: internal IPAM admin RPC — TODO #35)

## Сводка

| Сервис | Cases | Assertions | Failed | Requests | % к 100/рес |
|---|---|---|---|---|---|
| subnet | 131 | 905 | 0 | 644 | **131%** ✅ |
| network | 106 | 380 | 0 | 255 | **106%** ✅ |
| address | 100 | 346 | 0 | 233 | **100%** ✅ |
| security-group | 96 | 514 | 0 | 366 | 96% |
| route-table | 91 | 434 | 0 | 309 | 91% |
| gateway | 90 | 264 | 0 | 179 | 90% |
| private-endpoint | 68 | 261 | 0 | 195 | 68% |
| internal-pool | 26 | 133 | 0 | 81 | (admin) |
| internal-region-zone | 15 | 77 | 0 | 40 | (admin) |
| internal-cloud | 4 | 31 | 0 | 17 | (admin) |
| operation | 4 | 16 | 0 | 7 | (n/a) |
| **Итого** | **731** | **3361** | **0** | **2326** | — |

**100% PASS**. v16 добавил покрытие internal/admin-only IPAM RPC
(`InternalAddressPoolService` / `InternalRegion`/`InternalZone`/`InternalCloud`) —
kacho-only RPC проброшены через api-gateway cluster-internal mux, возвращают
ресурсы напрямую (не Operation). Новых FINDINGs: 3 (007/008/009 — informational,
все «фактическое поведение задокументировано в кейсе»).

> Деплоймент-замечание: suite требует `KACHO_VPC_DEFAULT_SG_INLINE=true`
> (default) — `*-LSG-CRUD-DEFAULT-SG` / `*-DEL-STATE-DEFAULT-SG` проверяют
> авто-создание default SG. При `=false` (load-test config) эти кейсы краснеют.
> internal-* кейсы используют seeded `ru-central1` region / `ru-central1-{a,b,c,d}`
> zones / `default-ru-central1-a` pool как readonly-фикстуры (не трогают),
> остальное — runId-суффиксованные throwaway-ресурсы с self-cleanup.

## Эволюция

| Версия | Cases | Assertions | Среднее/рес | % target |
|---|---|---|---|---|
| v1 | 89 | 467 | 11 | 11% |
| v11 | 578 | 2528 | 82 | 82% |
| v12 (FK RESTRICT delete) | 597 | 2616 | 85 | 85% |
| v13 (Req/Immutable matrix + CIDR pack) | 624 | 2744 | 89 | 89% |
| v14 (pairwise + security probes + lifecycle) | 685 | 3107 | 97 | 97% |
| v15 (FINDING-005 fix → SUB-CR-NEG-DUP-NAME; PE addressSpec.subnetId) | 686 | 3120 | 97 | 97% |
| **v16 (internal IPAM admin RPC — TODO #35: internal-pool/-region-zone/-cloud)** | **731** | **3361** | — | — |

## Sкилл-mapping (testing-product-coach §3, §4)

| Техника | Реализация |
|---|---|
| §3.1 ECP | ✅ `ecp_name_block`, `ecp_description_block`, `ecp_labels_block` |
| §3.2 BVA | ✅ `crud_list_bva_block`, pagesize 0/1/1000/1001/10000 |
| §3.3 Decision Tables | ✅ `required_fields_matrix`, `immutable_fields_matrix`, `updatemask_decision_table` |
| §3.4 State Transition | ✅ STATE class, immutable, idempotent move-self |
| §3.5 Pairwise | ✅ `pairwise_subnet_pack` (zone × prefix × dhcp, 9 кейсов из 18) |
| §3.6 Cause-Effect | ✅ имплицитно через decision tables |
| §3.7 Use-case | ✅ `conformance_lifecycle_pack` — full CRUD-цикл |
| §3.8 Error Guessing | ✅ `malformed_body_block`, `headers_content_type_block`, edge cases |
| §3.9 Exploratory | manual — не в автомате |
| §3.10 Property-Based | ✅ `idempotency_block`, `pagination_roundtrip` |
| §3.11 Risk-Based | ✅ priority P0..P3 tagging |
| §4.1 Smoke | ✅ P0/P1 кейсы — фактический smoke |
| §4.2 Functional regression | ✅ полная suite |
| §4.3 Conformance | ✅ CONF class verbatim text + lifecycle |
| §4.4 Performance | ✅ `perf_baseline_block` (response_time < 500ms) |
| §4.5-4.8 Load/Stress/Soak/Spike | → перенесено в k6 (отдельный setup) |
| §4.9 Chaos | → backlog |
| §4.10 Security | ✅ `security_injection_block` (SQLi/XSS/cmd/path traversal × 7) |
| §4.11 Compatibility | → backlog |
| §4.12 Migration | covered внешними тестами |
| §4.13 DR | → backlog |
| §4.14 Exploratory | manual |

## Findings

Найденные баги / расхождения — заводятся в GitHub Issues (`PRO-Robotech/kacho-vpc`, см.
`kacho-vpc/CLAUDE.md` §14.4); by-design расхождения с verbatim-YC — `docs/architecture/07-known-divergences.md`.
Отдельного bug-map / FINDING-реестра больше нет.
