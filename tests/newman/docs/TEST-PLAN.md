# Test Plan — newman coverage map (актуально на 2026-05-13)

> Новый кейс ОБЯЗАН пройти `scripts/validate-cases.py` (hard-fail в CI до newman:
> дубль case-id; кейс не зафиксирован в `CASES-INDEX.md` / не помечен `# index: <ref>`).
> Новый уникальный паттерн → запись в `CASES-INDEX.md` (+ при необходимости `REQ-*` в
> `PRODUCT-REQUIREMENTS.md`, апдейт этого файла + `RESULTS.md`).

Карта `(сервис, RPC) → классы → факт реализации`. Статусы:
`□` не начато, `◐` частично (есть happy ИЛИ negative), `■` базовое
покрытие (≥1 happy + ≥1 negative), `▣` расширенное (с BVA/STATE/CONF).

## NetworkService (10 RPC)

| RPC | Классы покрыто | Cases | Статус |
|---|---|---|---|
| Get | NEG (garbage→404), CONF, AUTHZ | NET-GET-NEG-NF, NET-GET-NEG-EMPTY-ID | ▣ |
| List | CRUD, VAL (project req), AUTHZ, PAGE (4 BVA + token) | NET-LST-* (5) | ▣ |
| Create | CRUD, VAL (project req), NEG (project NF, dup name), CONC | NET-CR-* (4) | ▣ |
| Update | CRUD (desc patch), STATE (immutable project_id), AUTHZ (sync-NF) | NET-UPD-* (3) | ▣ |
| Delete | NEG (sync-NF) | NET-DEL-AUTHZ-NF-SYNC | ◐ |
| ~~Move~~ | удален | — | — |
| ListSubnets | CRUD (empty) | NET-LSUB-CRUD-EMPTY | ◐ |
| ListSecurityGroups | CRUD (default-SG check) | NET-LSG-CRUD-DEFAULT-SG | ◐ |
| ListRouteTables | CRUD (empty) | NET-LRT-CRUD-EMPTY | ◐ |
| ListOperations | CRUD (contains create-op) | NET-LOP-CRUD-OK | ◐ |

**Coverage: 10/10 RPC (100%) хотя бы 1 кейс.**

## SubnetService (11 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | NEG | SUB-GET-NEG-NOT-FOUND | ◐ |
| List | CRUD, VAL (project req) | SUB-LST-* (2) | ■ |
| Create | CRUD, VAL (zone req, zone unknown, cidr req, hostbits), NEG (net NF, overlap) | SUB-CR-* (7) | ▣ |
| Update | AUTHZ-sync-NF, STATE (immutable CIDR) | SUB-UPD-* (2) | ■ |
| Delete | AUTHZ-sync-NF | SUB-DEL-AUTHZ-NF-SYNC | ◐ |
| ~~Move~~ | удален | — | — |
| AddCidrBlocks | CRUD-OK | SUB-ACB-CRUD-OK | ◐ |
| RemoveCidrBlocks | (planned) | — | □ |
| ~~Relocate~~ | удален | — | — |
| ListUsedAddresses | CRUD (empty) | SUB-LUA-CRUD-OK | ◐ |
| ListOperations | CRUD | SUB-LOP-CRUD-OK | ◐ |

**Coverage: 9/11 RPC (82%).**

## AddressService (9 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | NEG | ADR-GET-NEG-NF | ◐ |
| List | CRUD, VAL (project req) | ADR-LST-* (2) | ■ |
| Create | CRUD (int + ext), VAL (oneof, both-spec), NEG (subnet NF) | ADR-CR-* (5) | ▣ |
| Update | AUTHZ-sync-NF | ADR-UPD-AUTHZ-NF-SYNC | ◐ |
| Delete | AUTHZ-sync-NF | ADR-DEL-AUTHZ-NF-SYNC | ◐ |
| ~~Move~~ | удален | — | — |
| GetByValue | NEG (security-NF) | ADR-GBV-NEG-NF | ◐ |
| ListBySubnet | CRUD | ADR-LBS-CRUD-OK | ◐ |
| ListOperations | CRUD | ADR-LOP-CRUD-OK | ◐ |

**Coverage: 8/9 RPC (89%).**

## RouteTableService (7 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | NEG | RT-GET-NEG-NF | ◐ |
| List | CRUD, VAL (project req) | RT-LST-* (2) | ■ |
| Create | CRUD, VAL (network req), NEG (network NF) | RT-CR-* (3) | ▣ |
| Update | AUTHZ-sync-NF | RT-UPD-AUTHZ-NF-SYNC | ◐ |
| Delete | AUTHZ-sync-NF | RT-DEL-AUTHZ-NF-SYNC | ◐ |
| ~~Move~~ | удален | — | — |
| ListOperations | CRUD | RT-LOP-CRUD-OK | ◐ |

**Coverage: 6/7 RPC (86%).**

## SecurityGroupService (9 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | NEG | SG-GET-NEG-NF | ◐ |
| List | CRUD, VAL (project req) | SG-LST-* (2) | ■ |
| Create | CRUD, VAL (network req) | SG-CR-* (2) | ■ |
| Update | AUTHZ-sync-NF | SG-UPD-AUTHZ-NF-SYNC | ◐ |
| Delete | AUTHZ-sync-NF | SG-DEL-AUTHZ-NF-SYNC | ◐ |
| ~~Move~~ | удален | — | — |
| UpdateRules | CRUD, STATE (rules append) | SG-URL-CRUD-OK | ◐ |
| UpdateRule | (planned) | — | □ |
| ListOperations | CRUD | SG-LOP-CRUD-OK | ◐ |

**Coverage: 7/9 RPC (78%).**

## GatewayService (7 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | NEG | GW-GET-NEG-NF | ◐ |
| List | CRUD, VAL | GW-LST-* (2) | ■ |
| Create | CRUD, VAL (project req) | GW-CR-* (2) | ■ |
| Update | AUTHZ-sync-NF | GW-UPD-AUTHZ-NF-SYNC | ◐ |
| Delete | AUTHZ-sync-NF | GW-DEL-AUTHZ-NF-SYNC | ◐ |
| ~~Move~~ | удален | — | — |
| ListOperations | CRUD | GW-LOP-CRUD-OK | ◐ |

**Coverage: 6/7 RPC (86%).**

## NetworkInterfaceService (8 RPC) — first-class ресурс

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | CRUD (lean-проекция) | NIC-CR-CRUD-OK (get-after-create) | ◐ |
| List | CRUD (lean, no infra-fields) | NIC-LIST-OK | ◐ |
| Create | CRUD, NEG (bad subnet, dup name), + with-addr / with-v6-addr / with-unbound-sg | NIC-CR-* (6) | ▣ |
| Update | CRUD (name/labels/sg via mask) | NIC-UPD-OK | ◐ |
| Delete | CRUD-OK | NIC-DEL-OK | ■ |
| ~~AttachToInstance~~ | удален | — | — |
| ~~DetachFromInstance~~ | удален | — | — |
| ListOperations | (planned) | — | □ |

**Coverage: 5/6 RPC (83%).** Attach/DetachToInstance RPC удалены (NIC + used_by-колонки остаются). Связанный кейс в address.py/network.py/subnet.py: `ADDR-DEL-NEG-USED-BY-NIC`, `NET-DEL-NEG-HAS-SUBNET-WITH-NIC`, `SUB-DEL-NEG-HAS-NIC`, `NET-SUBNET-ADDR-NIC-DELETE-CHAIN`.

## OperationService (1 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | CRUD, NEG (invalid prefix → 400, valid prefix → 404) | OP-GET-* (3) | ▣ |

**Coverage: 1/1 RPC (100%).**

## InternalAddressPoolService (admin/IPAM — kacho-only, prefix `IPL-*`)

Split-shape (`v4_cidr_blocks` + `v6_cidr_blocks`) — добавлено 16 новых IPL-* кейсов
(в `cases/internal-pool.py`), 2 рефакторено (IPL-CR-CRUD-V4-OK, IPL-CR-VAL-BOTH-EMPTY) +
2 ADR-CR-EXT-FALLTHROUGH-V4/V6 в `cases/address.py` (cascade family-skip post-split).

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Create | CRUD (v4-only / v6-only / dual-stack), VAL (both-empty, cross-family, host-bits), NEG (CIDR-overlap) | IPL-CR-CRUD-V4-OK / -V6-OK / -DS-OK, IPL-CR-VAL-BOTH-EMPTY / -CROSS-V4-IN-V6 / -BAD-CIDR-HOSTBITS / -MISSING-KIND / -MISSING-NAME / -IPV6-CIDR, IPL-CR-NEG-DUP-DEFAULT / -BAD-ZONE / -OVERLAP | ▣ |
| Update | CRUD (description/labels/isDefault). CIDR через Update НЕ меняется | IPL-UPD-CRUD-OK | ■ |
| AddCidrBlocks / RemoveCidrBlocks | CRUD (add/remove + dedup), STATE (freelist sync), NEG (use-guard / not-found / empty overlap) | IPL-ADDCIDR-OK, IPL-RMCIDR-OK, IPL-RMCIDR-NEG-INUSE, IPL-ADDCIDR-NEG-OVERLAP | ▣ |
| Get / List / Delete | CRUD, NEG (NF) | IPL-LST-CRUD-OK, IPL-GET-NEG-NF, IPL-DEL-NEG-NF | ■ |
| GetUtilization / ListAddresses | CRUD, NEG | IPL-UTIL-CRUD-OK / -NEG-NF, IPL-LISTADDR-CRUD-OK / -EMPTY-OK | ▣ |
| ~~Check / ExplainResolution~~ | удалены | — | — |
| BindAsNetworkDefault / UnbindNetworkDefault (Network) | CRUD, IDM, STATE, NEG, **family-agnostic** | IPL-NETBIND-CRUD-OK / -NEG-NF, IPL-NETUNBIND-IDM-NOOP, IPL-BIND-FAMILY-AGNOSTIC | ▣ |
| ~~OverridePoolForAddress / UnbindAddressOverride (Address)~~ | удалены | — | — |
| Cascade resolve (косвенно через Address.Create) | CONF (family-skip на network_default/zone_default/global_default + dual-stack), NEG | IPL-RESOLVE-NETWORK-DEFAULT-FAMILY-SKIP / -DUALSTACK-OK, ADR-CR-EXT-FALLTHROUGH-V4 / -V6, ADR-CR-EXT-V6-FAMILY-FALLTHROUGH | ▣ |

**Coverage:** все surviving группы RPC покрыты. Check / ExplainResolution / OverridePoolForAddress / UnbindAddressOverride удалены вместе с override+selector cascade-шагами.

---

## Сводное покрытие

| Метрика | Значение |
|---|---|
| Публичных RPC | 60 |
| Покрыто (≥1 кейс) | 52 |
| **API surface coverage** | **87%** |
| Cases реализовано | 89 |
| Assertions выполняется | 467 |
| Passing | 467 |
| Pass rate | 100% |

## Backlog (приоритетный, для v2)

| Зона | RPC / класс | Priority |
|---|---|---|
| UpdateMask exhaustive | Update * с unknown field, immutable, empty mask | P1 |
| Pagination roundtrip | List * с page_token next-cycle | P2 |
| Filter syntax | List * с filter expression | P2 |
| Cross-project AUTHZ | PERMISSION_DENIED matrix | P0 |
| Concurrency P0 | Allocator race, parallel Create same name | P0 |
| Subnet RemoveCidrBlocks | CRUD + NEG-CANNOT-REMOVE-LAST | P1 |
| SG UpdateRule single | CRUD + NEG-RULE-NF | P1 |
| Network Delete с детьми | NEG-NETWORK-NOT-EMPTY | P1 |
| Conformance к контракту Kachō | byte-level error text (snapshot) | P0 для production |
