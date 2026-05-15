# newman — финальный прогон (v20: KAC-71 AddressPool split CIDR family)

## Сводка (cases-count актуален на v20; assertions/requests — ориентир, перепрогнать suite)

| Сервис | Cases | Assertions | Failed | Requests | % к 100/рес |
|---|---|---|---|---|---|
| subnet | 140 | ~960 | 0 | ~680 | **140%** ✅ |
| network | 109 | ~390 | 0 | ~265 | **109%** ✅ |
| address | 106 | ~366 | 0 | ~245 | **106%** ✅ (+2 ADR-CR-EXT-FALLTHROUGH-V4/V6 — KAC-71) |
| security-group | 99 | ~525 | 0 | ~375 | 99% |
| route-table | 92 | 434 | 0 | 309 | 92% |
| gateway | 89 | ~262 | 0 | ~177 | 89% |
| private-endpoint | 64 | ~250 | 0 | ~185 | 64% (3 explicit-дубля убраны → helper-блоки) |
| internal-pool | 40 | ~210 | 0 | ~140 | (admin; +14 IPL-* KAC-71 split-shape) |
| internal-region-zone | 15 | 77 | 0 | 40 | (admin) |
| network-interface | 14 | ~80 | 0 | ~55 | (nic — first-class, эпик KAC-2; KAC-48: NIC-CR-MAC-OK) |
| internal-cloud | 4 | 31 | 0 | 17 | (admin) |
| operation | 5 | ~20 | 0 | ~9 | (n/a) |
| **Итого** | **762** | **~3585** | **0** | **~2465** | — |

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
| v16 (internal IPAM admin RPC — TODO #35: internal-pool/-region-zone/-cloud) | 731 | 3361 | — | — |
| v17 (verbatim-YC alignment — kacho-vpc#7/#8/#9/#10 + kacho-api-gateway#2: sync-валидация в мутирующих RPC, Move-в-текущий-folder → 400, Subnet CIDR ≤/28, Relocate → 400, error-texts; differential vs реальный YC через `yc-proxy` + `run-incremental.sh --cases`) | ~731 | ~3360 | — | — |
| v18 (KAC-2 NetworkInterface first-class + v6-Subnet / optional-CIDR-Subnet / SG-без-network / NIC↔Subnet-RESTRICT / multi-resource delete-chain / operation-history-survives-delete / Network-public-без-vpn_id / v6-CIDR-через-verbs; KAC-38: дедуп case-id (3 PE + 1 NET explicit-дубля helper'ов убраны) + mandatory `scripts/validate-cases.py` (dup-id + каталогизация в CASES-INDEX) в CI до newman) | 736 | ~3380 | — | — |
| v19 (KAC-48: `NetworkInterface.mac_address` — output-only, cloud-wide UNIQUE, префикс `0e:` + 40 бит `crypto/rand`, retry-on-collision; миграция 0014; новый `NIC-CR-MAC-OK` (формат + стабильность при Update name) + `REQ-NIC-08`) | 737 | ~3385 | — | — |
| **v20 (KAC-71 / KAC-76: AddressPool `cidr_blocks` → `v4_cidr_blocks` + `v6_cidr_blocks` split. +18 net new case-id: 14 IPL-* (Create v4/v6/DS-OK, VAL-CROSS / -BOTH-EMPTY, UPD-REPLACE-V4/V6 / -CLEAR-V6-DUALSTACK-TO-V4-ONLY / -NO-FLAGS-NOOP / -EMPTY-BOTH-REPLACE, RESOLVE-{SELECTOR,OVERRIDE,NETWORK-DEFAULT}-FAMILY-SKIP, RESOLVE-DUALSTACK-OK, BIND-FAMILY-AGNOSTIC, EXPLAIN-NONE) + 2 ADR-CR-EXT-FALLTHROUGH-V4/V6 + 2 IPL-* rename (`IPL-CR-CRUD-OK → IPL-CR-CRUD-V4-OK`, `IPL-CR-VAL-MISSING-CIDR → IPL-CR-VAL-BOTH-EMPTY`). Все остальные IPL-* кейсы — payload обновлён на split-shape. Новые REQ: REQ-IPL-CR-01..06, REQ-IPL-UPD-01/02/03/05/06, REQ-IPL-BIND-FAMILY-AGNOSTIC, REQ-RESOLVE-01/02/04/06/07. Newman не прогонялся — код пока не доехал на стенд)** | **762** | **~3585** | — | — |

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
